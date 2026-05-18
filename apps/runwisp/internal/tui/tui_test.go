// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/home"
	"github.com/stretchr/testify/assert"
)

// TestPrintStartup_NeverLeaksPassword locks the banner's password-disclosure
// surface: the startup display must never print the plaintext value, only an
// instructional hint pointing operators at `runwisp password` or the TUI.
//
// Stderr capture is awkward, so we drive printStartupTo directly. PrintStartup
// is a one-liner around it, so the same guarantee holds for the public API.
func TestPrintStartup_NeverLeaksPassword(t *testing.T) {
	t.Run("ephemeral renders hint and never the value", func(t *testing.T) {
		const secret = "Kj2x9pQ7mN4vL8rT5wYz1c"
		var buf bytes.Buffer
		printStartupTo(&buf, uikit.StartupInfo{
			Version:           "0.0.0-test",
			Port:              9477,
			PasswordEphemeral: true,
			Password:          secret,
		})

		out := buf.String()
		assert.Contains(t, out, "Ephemeral password generated in memory",
			"ephemeral mode must surface the locating hint")
		assert.NotContains(t, out, secret,
			"plaintext ephemeral password must never appear in the banner")
		assert.NotContains(t, out, strings.Repeat("•", home.PasswordMaskWidth),
			"bullet mask belongs on the Home page; the banner has no Password field at all")
	})

	t.Run("operator-supplied password produces no hint and no value", func(t *testing.T) {
		const secret = "operator-supplied-secret"
		var buf bytes.Buffer
		printStartupTo(&buf, uikit.StartupInfo{
			Version:           "0.0.0-test",
			Port:              9477,
			PasswordEphemeral: false,
			Password:          secret,
		})

		out := buf.String()
		assert.NotContains(t, out, "🔑",
			"the key glyph is reserved for the ephemeral hint")
		assert.NotContains(t, out, "Ephemeral",
			"non-ephemeral mode must not mention an ephemeral password")
		assert.NotContains(t, out, secret,
			"the operator-supplied password must never reach the banner")
	})

	t.Run("empty ephemeral password still renders hint cleanly", func(t *testing.T) {
		var buf bytes.Buffer
		assert.NotPanics(t, func() {
			printStartupTo(&buf, uikit.StartupInfo{
				Version:           "0.0.0-test",
				Port:              9477,
				PasswordEphemeral: true,
				Password:          "",
			})
		})

		out := buf.String()
		assert.Contains(t, out, "Ephemeral password generated in memory",
			"empty-string edge case must still print the hint without short-circuiting")
	})
}

// --- printStartupTo branch coverage ---

func TestPrintStartupTo_WithTasks(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version: "0.0.0-test",
		Tasks: []model.TaskBrief{
			{Name: "backup", Cron: "0 3 * * *"},
			{Name: "healthcheck"},
		},
	})
	out := buf.String()
	assert.Contains(t, out, "Tasks")
	assert.Contains(t, out, "backup")
	assert.Contains(t, out, "healthcheck")
	// Two tasks: first gets ├─ prefix, last gets └─.
	assert.Contains(t, out, "├─")
	assert.Contains(t, out, "└─")
}

func TestPrintStartupTo_TaskKindService(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version: "0.0.0-test",
		Tasks: []model.TaskBrief{
			{Name: "worker", Kind: model.KindService, Instances: 3},
		},
	})
	out := buf.String()
	assert.Contains(t, out, "service x3")
}

func TestPrintStartupTo_TaskManualTrigger(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version: "0.0.0-test",
		Tasks: []model.TaskBrief{
			{Name: "deploy", Kind: model.KindTask, Cron: ""},
		},
	})
	out := buf.String()
	assert.Contains(t, out, "manual")
}

func TestPrintStartupTo_WithScheduleWarnings(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version:          "0.0.0-test",
		ScheduleWarnings: []string{"task foo has invalid cron", "task bar overlaps"},
	})
	out := buf.String()
	assert.Contains(t, out, "task foo has invalid cron")
	assert.Contains(t, out, "task bar overlaps")
}

func TestPrintStartupTo_WithFingerprint(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version:     "0.0.0-test",
		Fingerprint: "abc123xyz",
	})
	out := buf.String()
	assert.Contains(t, out, "abc123xyz")
}

func TestPrintStartupTo_WithTimezoneAndSource(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version:        "0.0.0-test",
		Timezone:       "Europe/Bratislava",
		TimezoneSource: "config",
	})
	out := buf.String()
	assert.Contains(t, out, "Europe/Bratislava")
	assert.Contains(t, out, "config")
}

func TestPrintStartupTo_WithTimezoneNoSource(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version:        "0.0.0-test",
		Timezone:       "UTC",
		TimezoneSource: "",
	})
	out := buf.String()
	assert.Contains(t, out, "UTC")
}

func TestPrintStartupTo_WithUsingDemo(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version:   "0.0.0-test",
		UsingDemo: true,
	})
	out := buf.String()
	assert.Contains(t, out, "demo task")
}

func TestPrintStartupTo_WithCrashedRuns(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version:     "0.0.0-test",
		CrashedRuns: 5,
	})
	out := buf.String()
	assert.Contains(t, out, "5 crashed runs")
}

func TestPrintStartupTo_WithCatchUpTriggered(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version:          "0.0.0-test",
		CatchUpTriggered: 3,
	})
	out := buf.String()
	assert.Contains(t, out, "3 catch-up runs")
}

func TestPrintStartupTo_WebUIDisabled(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version:       "0.0.0-test",
		WebUIDisabled: true,
	})
	out := buf.String()
	assert.Contains(t, out, "Web UI disabled")
}

func TestPrintStartupTo_WithPort(t *testing.T) {
	var buf bytes.Buffer
	printStartupTo(&buf, uikit.StartupInfo{
		Version: "0.0.0-test",
		Port:    8080,
	})
	out := buf.String()
	assert.Contains(t, out, "8080")
}

// --- printCapabilitiesSection ---

func TestPrintCapabilitiesSection_AvailableAndUnavailable(t *testing.T) {
	var buf bytes.Buffer
	printCapabilitiesSection(&buf, []model.CapInfo{
		{Name: "Shell", Available: true},
		{Name: "Docker", Available: false},
		{Name: "HTTP", Available: true},
	})
	out := buf.String()
	assert.Contains(t, out, "Shell")
	assert.Contains(t, out, "Docker")
	assert.Contains(t, out, "HTTP")
}

func TestPrintCapabilitiesSection_Empty(t *testing.T) {
	var buf bytes.Buffer
	printCapabilitiesSection(&buf, nil)
	// Should render (possibly empty line) without panicking.
	assert.NotEmpty(t, buf.String())
}

// --- printTasksSection ---

func TestPrintTasksSection_Empty(t *testing.T) {
	var buf bytes.Buffer
	printTasksSection(&buf, nil)
	// No output for an empty task list.
	assert.Empty(t, buf.String())
}

func TestPrintTasksSection_SingleTask(t *testing.T) {
	var buf bytes.Buffer
	printTasksSection(&buf, []model.TaskBrief{
		{Name: "only-task", Cron: "*/5 * * * *"},
	})
	out := buf.String()
	assert.Contains(t, out, "only-task")
	// Single task: only └─ prefix used.
	assert.Contains(t, out, "└─")
	assert.NotContains(t, out, "├─")
}

func TestPrintTasksSection_MultipleTasks(t *testing.T) {
	var buf bytes.Buffer
	printTasksSection(&buf, []model.TaskBrief{
		{Name: "first", Cron: "0 * * * *"},
		{Name: "second"},
		{Name: "third", Kind: model.KindService, Instances: 2},
	})
	out := buf.String()
	assert.Contains(t, out, "first")
	assert.Contains(t, out, "second")
	assert.Contains(t, out, "third")
}

// --- printPendingRunsSection ---

func TestPrintPendingRunsSection_Zero(t *testing.T) {
	var buf bytes.Buffer
	printPendingRunsSection(&buf, uikit.PendingRunsSummary{Total: 0})
	assert.Empty(t, buf.String())
}

func TestPrintPendingRunsSection_AllCounts(t *testing.T) {
	var buf bytes.Buffer
	printPendingRunsSection(&buf, uikit.PendingRunsSummary{
		Total:   10,
		Resumed: 3,
		Queued:  2,
		Skipped: 4,
		Failed:  1,
	})
	out := buf.String()
	assert.Contains(t, out, "3 resumed")
	assert.Contains(t, out, "2 re-queued")
	assert.Contains(t, out, "4 skipped")
	assert.Contains(t, out, "1 failed")
	assert.Contains(t, out, "Pending runs")
}

func TestPrintPendingRunsSection_OnlyResumed(t *testing.T) {
	var buf bytes.Buffer
	printPendingRunsSection(&buf, uikit.PendingRunsSummary{Total: 5, Resumed: 5})
	out := buf.String()
	assert.Contains(t, out, "5 resumed")
	assert.NotContains(t, out, "re-queued")
	assert.NotContains(t, out, "skipped")
	assert.NotContains(t, out, "failed")
}

func TestPrintPendingRunsSection_OnlyQueued(t *testing.T) {
	var buf bytes.Buffer
	printPendingRunsSection(&buf, uikit.PendingRunsSummary{Total: 2, Queued: 2})
	out := buf.String()
	assert.Contains(t, out, "2 re-queued")
}

// --- printDotField ---

func TestPrintDotField_ShortLabel(t *testing.T) {
	var buf bytes.Buffer
	printDotField(&buf, "DB", "/data/runwisp.db")
	out := buf.String()
	assert.Contains(t, out, "DB")
	assert.Contains(t, out, "/data/runwisp.db")
	// Dots must appear between label and value.
	assert.Contains(t, out, "·")
}

func TestPrintDotField_LabelAtPad(t *testing.T) {
	// Label exactly fieldPad (14) chars → at least 1 dot via max(1, ...).
	var buf bytes.Buffer
	printDotField(&buf, "ConfigFilePath", "/etc/rw.toml")
	out := buf.String()
	assert.Contains(t, out, "ConfigFilePath")
	assert.Contains(t, out, "·")
}

func TestPrintDotField_LongLabel(t *testing.T) {
	// Label longer than fieldPad → max(1, negative) = 1 dot.
	var buf bytes.Buffer
	printDotField(&buf, "AVeryLongLabelName", "value")
	out := buf.String()
	assert.Contains(t, out, "AVeryLongLabelName")
	assert.Contains(t, out, "value")
}

// --- printStartupTo minimal ---

// TestPrintStartupTo_Minimal verifies that a bare StartupInfo (only Version)
// produces output that contains "RunWisp" and does not panic.
func TestPrintStartupTo_Minimal(t *testing.T) {
	var buf bytes.Buffer
	assert.NotPanics(t, func() {
		printStartupTo(&buf, uikit.StartupInfo{Version: "0.1.0"})
	})
	assert.Contains(t, buf.String(), "RunWisp")
}

// --- SetLogOutput ---

// TestSetLogOutput_NonNil verifies that SetLogOutput with a non-nil writer
// reconfigures slog so that subsequent log lines go to that writer.
// We write a log line and confirm it appears in the buffer.
func TestSetLogOutput_NonNil(t *testing.T) {
	var buf bytes.Buffer
	SetLogOutput(&buf)

	slog.Info("test-marker-xyz")
	assert.Contains(t, buf.String(), "test-marker-xyz",
		"SetLogOutput must redirect slog output to the provided writer")
}

// --- PrintShutdown / PrintShutdownComplete ---
// These write directly to os.Stderr; we only verify they don't panic since
// capturing os.Stderr in a unit test is not worth the complexity.

func TestPrintShutdown_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() { PrintShutdown() })
}

func TestPrintShutdownComplete_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() { PrintShutdownComplete() })
}
