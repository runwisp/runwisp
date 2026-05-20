// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
}

// Installer is implemented per-OS. The interface keeps the cobra
// commands OS-neutral and is the natural seam for unit tests against
// FakeFS / FakeOSCmd.
type Installer interface {
	// Render returns the unit/plist body without touching disk.
	// Powers `service install --print`.
	Render(opts InstallOptions) ([]byte, error)

	ComputePlan(ctx context.Context, opts InstallOptions) (Plan, error)
	Install(ctx context.Context, opts InstallOptions, out io.Writer) error
	ComputeUninstallPlan(ctx context.Context, opts UninstallOptions) (Plan, error)
	Uninstall(ctx context.Context, opts UninstallOptions, out io.Writer) error
	Status(ctx context.Context, opts InstallOptions) (Status, error)
}

// ErrUnsupported is returned by `New` on platforms without an installer.
var ErrUnsupported = errors.New("autostart: no installer for this OS — see https://docs.runwisp.com/operations/autostart/ for manual setup")

// ErrConflict means the unit file exists but is missing the managed
// marker. The caller should suggest `--force` or manual cleanup.
var ErrConflict = errors.New("autostart: unit file is not managed by runwisp — use --force to overwrite")

// ErrAlreadyRunning means a daemon is already running against the
// same data dir; installing the unit now would race the live process.
var ErrAlreadyRunning = errors.New("autostart: a daemon is already running against this data dir")

// ErrConfigMissing means runwisp.toml does not exist. `service install`
// never creates one — the operator runs `runwisp` interactively first.
var ErrConfigMissing = errors.New("autostart: runwisp.toml is missing — run `runwisp` interactively to create one first")
