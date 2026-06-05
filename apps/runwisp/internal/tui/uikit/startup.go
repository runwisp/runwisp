// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package uikit

import "github.com/runwisp/runwisp/internal/model"

// PendingRunsSummary describes what happened when resuming pending runs.
type PendingRunsSummary struct {
	Total   int
	Resumed int
	Queued  int
	Skipped int
	Failed  int
}

// StartupInfo gathers everything needed for the startup display.
//
// Timezone and TimezoneSource render the resolved scheduler zone in the
// banner. TimezoneSource is "config" when the operator pinned [scheduler]
// timezone explicitly, "system" when it was detected from the host. Empty
// values omit the line so the trim-down `runwisp tui` invocation still
// renders cleanly.
type StartupInfo struct {
	Version    string
	ConfigPath string
	DataDir    string
	DBPath     string
	LogDir     string
	Port       int
	// ListenURL is the accurate, operator-reachable base URL of the Web UI
	// ([daemon] external_url when set, else http://<bind-host>:<port> with
	// wildcard binds mapped to localhost). Empty when the Web UI is disabled.
	ListenURL string

	Fingerprint    string
	UsingDemo      bool
	Capabilities   []model.CapInfo
	Tasks          []model.TaskBrief
	Timezone       string
	TimezoneSource string

	PasswordEphemeral bool
	Password          string
	// AuthDisabled is true when the daemon runs with RUNWISP_NO_AUTH — there
	// is no password to show; the Home header renders "disabled" instead.
	AuthDisabled bool

	CloudEnabled  bool
	WebUIDisabled bool
	// ServiceManaged is true when the daemon runs under systemd / launchd.
	// The quit dialog then drops its "Shut Down" option in favour of a
	// `runwisp stop` hint, so the TUI never fights the service manager.
	ServiceManaged bool
	// ConfigStale is true when runwisp.toml (or an env_file) changed on disk
	// after the daemon loaded it. Kept current by the TUI's periodic
	// /api/info poll; renders as a "restart to apply" notice in the header.
	ConfigStale bool

	// Headless is set when the daemon runs without an interactive TUI. The
	// startup banner renders one extra dim line ("Press Ctrl+C to stop.") in
	// that case, replacing what used to be a separate timestamped slog INFO.
	Headless bool

	ScheduleWarnings []string
	// InitWarnings holds non-fatal startup hiccups (notify subsystem failures,
	// crashed-run marking errors, scheduler start hiccups). Rendered inside
	// the banner with the same ⚠ prefix as schedule warnings so they appear
	// in the banner rather than fragmenting it from above.
	InitWarnings     []string
	CrashedRuns      int64
	PendingRuns      PendingRunsSummary
	CatchUpTriggered int
}
