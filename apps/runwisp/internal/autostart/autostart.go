// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package autostart wires the daemon into the host's init system
// (systemd user unit on Linux/WSL, launchd LaunchAgent on macOS).
//
// The package is OS-aware via build tags. Common types and helpers
// (plan computation, path resolution, prompts) live in OS-neutral
// files; the actual installer is selected at compile time by
// systemd_linux.go / launchd_darwin.go.
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

// Step is one entry in the confirmation banner. Description is
// rendered verbatim; the "← needs sudo" suffix lives in Description
// when relevant.
type Step struct {
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

	// CronFailsafe is the rendered failsafe unit a take-over installs next to
	// the service, so a RunWisp that keeps failing hands the jobs back to
	// CronUnit. Empty when there is no cron to hand back to.
	CronFailsafe string

	// cronPrior is, on an uninstall plan, the state to restore CronUnit to
	// (a cronPrior* value; empty for a unit that predates the marker).
	cronPrior string
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
	// StopTimeout is how long the service manager waits for the daemon to
	// exit before it SIGKILLs it: the daemon's whole shutdown budget for the
	// config's [daemon] shutdown_timeout.
	StopTimeout time.Duration
	// Force overrides PlanConflict on install.
	Force bool
	// TakeOverCron requests that ComputePlan/Install also stop and mask
	// the system cron unit once RunWisp is confirmed running (Linux
	// system-wide installs only). internal/cutover decides whether the
	// preconditions hold; this package only executes the mechanics.
	TakeOverCron bool

	// PreConfirmed says the caller (internal/cutover) already showed a plan
	// covering every step and took the operator's consent, so Install skips its
	// own banner and "Proceed?".
	PreConfirmed bool

	// maskedCronUnit is the cron unit name recorded in the rendered unit's
	// marker comment. Unexported so a marker only ever comes from ComputePlan's
	// discovery (or an existing unit's marker), never from a caller.
	maskedCronUnit string
	// cronPriorState is resolved alongside maskedCronUnit: the state that
	// cron unit was in before RunWisp first masked it.
	cronPriorState string
}

// UninstallOptions is the input to Uninstall.
type UninstallOptions struct {
	// Purge requests that the data dir be removed too. Uninstall asks for the
	// literal-word confirmation itself.
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

// Installer is implemented per-OS (systemd, launchd). It keeps the cobra
// commands OS-neutral and is the seam for unit tests.
type Installer interface {
	// Render returns the unit/plist body without touching disk
	// (`service install --print`).
	Render(opts InstallOptions) ([]byte, error)

	ComputePlan(ctx context.Context, opts InstallOptions) (Plan, error)
	Install(ctx context.Context, opts InstallOptions, out io.Writer) error
	Uninstall(ctx context.Context, opts UninstallOptions, out io.Writer) error
	Status(ctx context.Context, opts InstallOptions) (Status, error)

	// Stop and Restart go through the init system rather than signalling the
	// PID, so the manager's view of the service stays in sync. Stop leaves the
	// unit installed and enabled.
	Stop(ctx context.Context, opts InstallOptions) error
	Restart(ctx context.Context, opts InstallOptions) error

	// EnsurePasswordDropIn writes a 0600 drop-in beside the managed unit that
	// sets RUNWISP_PASSWORD, so the service has a stable Web UI password. It
	// never rotates: an existing drop-in is left alone and wrote is false.
	// launchd has no drop-ins and returns ("", false, nil).
	EnsurePasswordDropIn(ctx context.Context, opts InstallOptions, password string) (path string, wrote bool, err error)

	// SupportsPasswordDropIn reports whether EnsurePasswordDropIn can write a
	// drop-in on this OS, so a --dry-run can describe what install will do.
	SupportsPasswordDropIn() bool

	// WriteEnvDropIn replaces a 0600 drop-in holding one Environment line per
	// entry in vars (sorted), carrying the operator's RUNWISP_* environment into
	// the service. Every call rewrites the file to match vars exactly; empty
	// vars removes it. The returned DropInChange separates a removal from a
	// write because removing a captured RUNWISP_AUTH=off silently turns auth
	// back on. launchd returns ("", DropInUnchanged, nil).
	WriteEnvDropIn(ctx context.Context, opts InstallOptions, name string, vars map[string]string) (path string, change DropInChange, err error)

	// CronStatus reports the host's system cron unit and whether it is running.
	// An empty unit means there is nothing to take over; that is a legitimate
	// answer, not an error.
	CronStatus(ctx context.Context) (unit string, active bool, err error)
}

// ServiceManagedEnv is set to "1" in the generated systemd unit and
// launchd plist so a daemon can tell it was started by the init system
// rather than by hand. Quitting such a daemon via SIGTERM would just get
// it respawned (or leave the manager's view stale), so UIs use this to
// steer the operator toward `runwisp stop` / `systemctl --user stop`.
// A hand-written unit must set this itself to be recognized as service-managed.
const ServiceManagedEnv = "RUNWISP_SERVICE_MANAGED"

// DropInChange reports what WriteEnvDropIn did to the on-disk drop-in, so a
// caller can tell a normal write apart from a removal — the latter can
// silently revert a previously-captured RUNWISP_AUTH=off (or other setting)
// back to its default and deserves a much louder message than "saved".
type DropInChange int

const (
	// DropInUnchanged means the file already matched vars (or was already
	// absent) — no write, no removal, no reload, no restart needed.
	DropInUnchanged DropInChange = iota
	// DropInWritten means the drop-in was created or its content refreshed.
	DropInWritten
	// DropInRemoved means an existing drop-in was deleted because this call
	// captured no RUNWISP_* vars — any setting it carried (RUNWISP_AUTH=off
	// included) has now reverted to its default.
	DropInRemoved
)

// EnvDropInName is the "<unit>.d/<name>" drop-in `service install` writes the
// operator's captured RUNWISP_* environment into via WriteEnvDropIn. Named to
// sort lexicographically after the password drop-in's filename
// ("password.conf" < "runwisp-env.conf"), so a systemd drop-in directory's
// last-wins merge lets an operator-supplied RUNWISP_PASSWORD captured in here
// override a previously generated fallback in password.conf, with no
// separate remove-on-change logic needed.
const EnvDropInName = "runwisp-env.conf"

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
		if !strings.HasPrefix(e, ServiceManagedEnv+"=") {
			out = append(out, e)
		}
	}
	return out
}

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
