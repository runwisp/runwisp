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

	Fingerprint    string
	UsingDemo      bool
	Capabilities   []model.CapInfo
	Tasks          []model.TaskBrief
	Timezone       string
	TimezoneSource string

	PasswordEphemeral bool
	Password          string

	CloudEnabled  bool
	WebUIDisabled bool

	ScheduleWarnings []string
	CrashedRuns      int64
	PendingRuns      PendingRunsSummary
	CatchUpTriggered int
}
