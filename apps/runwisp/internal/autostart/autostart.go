// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package autostart wires the daemon into the host's init system
// (systemd user unit on Linux/WSL, launchd LaunchAgent on macOS).
//
// The package is OS-aware via build tags. Common types and helpers
// (plan computation, path resolution, prompts) live in OS-neutral
// files; the actual installer is selected at compile time by
// systemd_linux.go / launchd_darwin.go / unsupported_other.go.
package autostart

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// ManagedMarker is the first line of every generated unit/plist. Its
// presence tells the installer the file is safe to overwrite; its
// absence means the file was hand-written and we must refuse.
const ManagedMarker = "# Managed by runwisp service install — DO NOT EDIT"

// PlanKind is what `service install` (or `service uninstall`) would do
// given the current state on disk.
type PlanKind int

const (
	// PlanNoop means the unit is already installed with matching
	// content and the desired enable/run state — no action needed.
	PlanNoop PlanKind = iota
	// PlanInstall means no unit exists; we will write one from scratch.
	PlanInstall
	// PlanUpdate means a managed unit exists but its content differs;
	// we will rewrite it (after showing a diff and confirming).
	PlanUpdate
	// PlanConflict means a unit exists without the managed marker —
	// the user wrote it by hand and `--force` is required to overwrite.
	PlanConflict
	// PlanUninstall means we will remove a managed unit.
	PlanUninstall
)

// String returns a short label for diagnostics and tests.
func (k PlanKind) String() string {
	switch k {
	case PlanNoop:
		return "noop"
	case PlanInstall:
		return "install"
	case PlanUpdate:
		return "update"
	case PlanConflict:
		return "conflict"
	case PlanUninstall:
		return "uninstall"
	default:
		return "unknown"
	}
}

// Action enumerates the externally-visible side effects an installer
// can take. The list is exhaustive so the confirmation banner is the
// truth about what `Apply` will do — there are no hidden steps.
type Action int

const (
	ActionWriteUnit Action = iota + 1
	ActionRemoveUnit
	ActionDaemonReload
	ActionEnableLinger
	ActionEnableService
	ActionDisableService
	ActionStartService
	ActionStopService
	ActionPrintWSLPostscript
	ActionLaunchctlBootstrap
	ActionLaunchctlBootout
	ActionStopCron
	ActionMaskCron
	ActionUnmaskCron
)

// Step is one entry in the confirmation banner. Description is
// rendered verbatim; the "← needs sudo" suffix lives in Description
// when relevant.
type Step struct {
	Action      Action
	Description string
}

// Plan captures everything the operator needs to see before
// approving an install or uninstall.
type Plan struct {
	Kind   PlanKind
	Reason string
	Steps  []Step

	// Resolved settings (shown in the banner).
	Binary  string
	Config  string
	DataDir string
	Port    int
	Host    string

	// Where the unit/plist will be written.
	UnitPath string

	// Generated unit content. Set on PlanInstall, PlanUpdate, and any
	// PlanNoop that hit the rendered path. Empty on PlanConflict.
	UnitContent string

	// On PlanUpdate, a unified diff of existing → new content.
	Diff string

	// LingerOn is the current loginctl linger state (Linux only).
	LingerOn bool

	// CronUnit is the systemd cron unit (e.g. "cron.service") this plan
	// will mask, or has already recorded masking, via its own
	// runwisp-masked-cron marker. Empty means no take-over is in play —
	// either the install was not asked to retire cron, or an
	// uninstall found no marker it can prove it wrote itself.
	CronUnit string
}

// InstallOptions is the input to ComputePlan / Install.
type InstallOptions struct {
	Binary  string
	Config  string
	DataDir string
	Port    int
	Host    string
	// System requests a system-wide unit (Linux only, advanced).
	System bool
	// Force overrides PlanConflict on install.
	Force bool
	// TakeOverCron requests that ComputePlan/Install also stop and mask
	// the system cron unit once RunWisp is confirmed running (Linux
	// system-wide installs only). internal/cutover decides whether the
	// preconditions hold; this package only executes the mechanics.
	TakeOverCron bool

	// PreConfirmed says the caller has already rendered a plan covering
	// every step below and taken the operator's consent, so Install must
	// not banner-and-ask again. internal/cutover sets it: its plan is a
	// superset of this one (it also writes the config), and asking twice
	// for one decision is how an operator learns to stop reading prompts.
	//
	// It suppresses the up-front "Proceed?" for a plan the caller showed.
	// Nothing further down asks either: the mask is spelled out in both that
	// plan and this package's own banner, so consent for it has always been
	// taken by the time any cron unit is touched.
	PreConfirmed bool

	// maskedCronUnit is the cron unit name to record in the rendered
	// unit's marker comment. It is unexported so only this package can
	// set it: ComputePlan resolves it (via discovery when TakeOverCron
	// is set, or by carrying forward an existing unit's marker
	// otherwise) on a local copy of InstallOptions before calling
	// renderUnit, so a caller outside the package can never force a
	// take-over marker into a rendered unit without going through the
	// real discovery/masking path.
	maskedCronUnit string
}

// UninstallOptions is the input to Uninstall.
type UninstallOptions struct {
	// Purge requests that the data dir be removed too. The caller is
	// responsible for confirming the literal-word prompt before
	// passing true.
	Purge bool
	// DataDir is the resolved data dir — needed by --purge so the
	// installer knows what to remove.
	DataDir string
	// Force overrides PlanConflict on uninstall (hand-edited unit).
	Force bool
	// System must match the System the unit was installed with — it
	// picks the unit path (system vs user dir) and the systemctl
	// invocation (sudo systemctl vs systemctl --user). Without it,
	// uninstalling a --system install looks at the user unit path,
	// finds nothing, and reports "Nothing to uninstall ✓" while the
	// system unit stays enabled and running.
	System bool
}

// Status answers `service status` — five rows of state on one screen.
type Status struct {
	OS string // "linux", "darwin", …

	UnitPath    string
	UnitExists  bool
	UnitManaged bool

	Installed bool // unit present and managed
	Autostart bool // enabled (will start on boot)
	Running   bool // service is currently active

	// Drift detection.
	ExpectedConfigHash string
	UnitConfigHash     string
	ExpectedBinarySHA  string
	BinaryOnDiskSHA    string

	Binary       string
	BinaryExists bool

	DataDir          string
	DataDirWritable  bool
	DataDirLastWrite time.Time

	Linger bool // loginctl linger; false on macOS (N/A)

	LastStart time.Time
	LogsHint  string // e.g. "journalctl --user -u runwisp.service"

	// CronUnit is set once this instance's unit carries a
	// runwisp-masked-cron marker — the standing post-cutover check.
	// Empty means this instance has never taken over cron.
	CronUnit string
	// CronMasked/CronActive are probed live against CronUnit when it is
	// non-empty. CronActive true after a take-over means cron came back
	// (e.g. an operator manually unmasked it) and jobs may be firing
	// twice.
	CronMasked bool
	CronActive bool
}

// Installer is implemented per-OS. The interface keeps the cobra
// commands OS-neutral and is the natural seam for unit tests against
// FakeFS / FakeRunner.
type Installer interface {
	// Render returns the unit/plist body without touching disk.
	// Powers `service install --print`.
	Render(opts InstallOptions) ([]byte, error)

	ComputePlan(ctx context.Context, opts InstallOptions) (Plan, error)
	Install(ctx context.Context, opts InstallOptions, out io.Writer) error
	ComputeUninstallPlan(ctx context.Context, opts UninstallOptions) (Plan, error)
	Uninstall(ctx context.Context, opts UninstallOptions, out io.Writer) error
	Status(ctx context.Context, opts InstallOptions) (Status, error)

	// Stop stops the managed daemon through the init system (systemctl
	// --user stop / launchctl kill SIGTERM). The unit stays installed and
	// enabled — it will come back on the next boot or `Restart`. Going
	// through the manager instead of signalling the PID directly keeps the
	// manager's view of the service in sync.
	Stop(ctx context.Context, opts InstallOptions) error
	// Restart restarts the managed daemon through the init system
	// (systemctl --user restart / launchctl kickstart -k).
	Restart(ctx context.Context, opts InstallOptions) error

	// CronStatus reports the host's system cron unit and whether it is
	// currently running. An empty unit name means there is nothing to take
	// over — no cron unit on this host, or an OS where masking cron is not
	// something RunWisp can do. It never errors just because cron is
	// absent; that is a legitimate answer.
	CronStatus(ctx context.Context) (unit string, active bool, err error)
}

// ServiceManagedEnv is set to "1" in the generated systemd unit and
// launchd plist so a daemon can tell it was started by the init system
// rather than by hand. Quitting such a daemon via SIGTERM would just get
// it respawned (or leave the manager's view stale), so UIs use this to
// steer the operator toward `runwisp stop` / `systemctl --user stop`.
// A hand-written unit must set this itself to be recognized as service-managed.
const ServiceManagedEnv = "RUNWISP_SERVICE_MANAGED"

// serviceEnvVars mark a process as init-system-managed. A daemon that runwisp
// spawns itself must not inherit these, or it self-reports as service-managed
// merely because the spawning shell happened to run under systemd.
var serviceEnvVars = []string{ServiceManagedEnv}

// RunningUnderServiceManager reports whether the current process was launched
// by an init system, via the ServiceManagedEnv marker our generated unit files
// set. We deliberately do not sniff systemd's INVOCATION_ID: systemd sets it on
// a unit's main process and it is inherited by every descendant — the login
// session, the terminal emulator, the shell — so a plain `runwisp daemon` typed
// into a desktop terminal would carry it and false-report as service-managed.
func RunningUnderServiceManager() bool {
	return os.Getenv(ServiceManagedEnv) == "1"
}

// WithoutServiceEnv returns env (in "KEY=VALUE" form, as from os.Environ) with
// the service-manager marker vars removed. Use it when spawning a daemon that
// runwisp itself launches: such a daemon is not init-managed, so it must not
// inherit a stray RUNWISP_SERVICE_MANAGED from the spawning environment.
func WithoutServiceEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if hasServiceEnvPrefix(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func hasServiceEnvPrefix(entry string) bool {
	for _, key := range serviceEnvVars {
		if strings.HasPrefix(entry, key+"=") {
			return true
		}
	}
	return false
}

// ErrUnsupported is returned by `New` on platforms without an installer.
var ErrUnsupported = errors.New("autostart: no installer for this OS — see https://docs.runwisp.com/operations/autostart/ for manual setup")

// ErrConflict means the unit file exists but is missing the managed
// marker. The caller should suggest `--force` or manual cleanup.
var ErrConflict = errors.New("autostart: unit file is not managed by runwisp — use --force to overwrite")

// ErrConfigMissing means runwisp.toml does not exist. `service install`
// never creates one — the operator runs `runwisp` interactively first.
var ErrConfigMissing = errors.New("autostart: runwisp.toml is missing — run `runwisp` interactively to create one first")

// ErrCronTakeoverUnsupported means InstallOptions.TakeOverCron was set on an OS
// without a systemd cron unit this package can mask. macOS's cron
// (com.vix.cron) lives under SIP-protected /System/Library/LaunchDaemons —
// nothing running as the operator's user can touch it — so the honest path
// is `runwisp import cron` followed by removing the crontab by hand.
//
// internal/cutover reports this as a plan blocker before it ever gets here (with
// the same manual route spelled out), so reaching this error means a caller
// bypassed the plan.
var ErrCronTakeoverUnsupported = errors.New("autostart: retiring cron is only supported on Linux with systemd — run `runwisp import cron` then remove the crontab yourself (`crontab -r`)")
