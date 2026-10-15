// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

func TestSummarizeTaskRuns_ClassifiesAndPicksLatestFailure(t *testing.T) {
	newest := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	older := newest.Add(-time.Hour)

	// Newest-first, as the fetch requests. IsFailure is the persisted
	// classification manager writes at termination — the TUI reads that bit
	// rather than re-deriving failure from the end reason.
	runs := []model.Run{
		{Status: model.PhaseRunning}, // other
		{Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonCrashed), IsFailure: true, EndedAt: &newest}, // failure (latest)
		{Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess)},                                    // success
		{Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonFailed), IsFailure: true, EndedAt: &older},   // failure
		{Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSkipped)},                                    // other
	}

	got := summarizeTaskRuns("alpha", runs, 99)

	if got.TaskName != "alpha" {
		t.Fatalf("task name: want alpha, got %q", got.TaskName)
	}
	if got.Total != 99 {
		t.Fatalf("total: want 99, got %d", got.Total)
	}
	if got.Window != len(runs) {
		t.Fatalf("window: want %d, got %d", len(runs), got.Window)
	}
	if got.Success != 1 {
		t.Fatalf("success: want 1, got %d", got.Success)
	}
	if got.Failed != 2 {
		t.Fatalf("failed: want 2, got %d", got.Failed)
	}
	if got.Other != 2 {
		t.Fatalf("other: want 2, got %d", got.Other)
	}
	if got.LastFailure == nil || !got.LastFailure.Equal(newest) {
		t.Fatalf("last failure: want %v, got %v", newest, got.LastFailure)
	}
}

func TestSummarizeTaskRuns_UsualUsage(t *testing.T) {
	cpu, mem := int64(1500), int64(32<<20)
	got := summarizeTaskRuns("alpha", []model.Run{{Status: model.PhaseEnded, CPUTimeMs: &cpu, PeakMemoryBytes: &mem}}, 1)
	if got.Usual == nil || *got.Usual != (uikit.RunUsage{CPUMs: 1500, MemBytes: 32 << 20}) {
		t.Fatalf("usual: want 1500ms / 32 MB, got %+v", got.Usual)
	}
	if got := summarizeTaskRuns("alpha", []model.Run{{Status: model.PhaseEnded}}, 1); got.Usual != nil {
		t.Fatalf("usual: want nil without measured runs, got %+v", got.Usual)
	}
}

func TestTaskDetailDialog_View_CommandTimeoutAndRetention(t *testing.T) {
	timeout := 12 * time.Hour
	keepRuns := 50
	d := NewTaskDetailDialog("backup", &model.Task{
		Name: "backup", Kind: model.KindTask, Cron: "29 3 * * *",
		Timeout: &timeout, LogMaxSize: 10 << 20, LogOnFull: "drop_old",
		KeepRuns: &keepRuns, KeepFor: 120 * 24 * time.Hour,
	})
	d.command = "/usr/local/bin/backup_opensense.sh"
	cpu, mem := int64(1500), int64(32<<20)
	d.ApplySummary(summarizeTaskRuns("backup", []model.Run{{Status: model.PhaseEnded, CPUTimeMs: &cpu, PeakMemoryBytes: &mem}}, 1))

	view := ansi.Strip(d.View(80, 40))
	for _, want := range []string{
		"Command", "/usr/local/bin/backup_opensense.sh",
		"Timeout", "12h",
		"Output and retention", "10 MB · drop_old", "Keep runs", "50", "Keep for", "120d",
		"Usual CPU", "2s", "Usual memory", "32 MB",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in %q", want, view)
		}
	}
}

func TestTaskDetailDialog_View_CommandPreviewFirstExpandsOnC(t *testing.T) {
	d := NewTaskDetailDialog("backup", &model.Task{Name: "backup", Kind: model.KindTask, Cron: "0 3 * * *"})
	d.ApplySummary(uikit.TaskSummaryMsg{TaskName: "backup", Total: 1, Window: 1, Success: 1})
	var cmd []string
	for i := range 30 {
		cmd = append(cmd, fmt.Sprintf("step-%02d", i))
	}
	d.command = strings.Join(cmd, "\n")

	// The inspector leads with the first three lines and points at the rest.
	view := ansi.Strip(d.View(80, 80))
	if strings.Index(view, "Command") > strings.Index(view, "Schedule") {
		t.Fatal("expected the command before the definition")
	}
	if !strings.Contains(view, "step-02") || strings.Contains(view, "step-03") {
		t.Fatalf("expected only the first three command lines:\n%s", view)
	}
	if !strings.Contains(view, "27 more lines") || !strings.Contains(view, "c full command") {
		t.Fatalf("expected a pointer to the full command:\n%s", view)
	}

	// c opens the full command on a short screen; it scrolls to the end.
	if _, closed := d.Update(tea.KeyPressMsg{Code: 'c', Text: "c"}); closed {
		t.Fatal("c should expand the command, not close")
	}
	full := ansi.Strip(d.View(160, 24))
	if !strings.Contains(full, "step-00") || strings.Contains(full, "step-29") || !strings.Contains(full, "↑/↓ scroll") {
		t.Fatalf("expected the top of the command and a scroll hint:\n%s", full)
	}
	d.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if bottom := ansi.Strip(d.View(160, 24)); !strings.Contains(bottom, "step-29") {
		t.Fatalf("expected the command's end in view:\n%s", bottom)
	}
	d.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if d.commandScroll.offset != d.commandScroll.maxScroll()-1 {
		t.Fatalf("expected the wheel to scroll up one row, offset %d", d.commandScroll.offset)
	}

	// esc steps back to the inspector instead of closing it.
	if _, closed := d.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); closed || d.commandOpen {
		t.Fatal("esc should return from the full command to the inspector")
	}
	if _, closed := d.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); !closed {
		t.Fatal("a second esc should close the inspector")
	}
}

func TestCommandPreviewLines_ClipsLongLine(t *testing.T) {
	got := ansi.Strip(strings.Join(commandPreviewLines(strings.Repeat("x", 100), 40), "\n"))
	if !strings.Contains(got, "…") || !strings.Contains(got, "press c for the full command") || strings.Contains(got, "more lines") {
		t.Fatalf("expected one clipped line and a pointer, got:\n%s", got)
	}
	if got := commandPreviewLines("short", 40); len(got) != 1 {
		t.Fatalf("expected a short command without a pointer, got %d lines", len(got))
	}
}

func TestTaskDetailDialog_View_PlainTaskHasNoCommandOrRetention(t *testing.T) {
	view := ansi.Strip(NewTaskDetailDialog("test", &model.Task{Name: "test", Cron: "0 3 * * *"}).View(80, 30))
	for _, absent := range []string{"Command", "Timeout", "Output and retention"} {
		if strings.Contains(view, absent) {
			t.Errorf("unexpected %q in %q", absent, view)
		}
	}
}

// An explicit timeout of 0 means "no timeout", so the inspector must not show
// it as a limit of "0s".
func TestTaskDetailDialog_View_ZeroTimeoutIsNoTimeout(t *testing.T) {
	zero := time.Duration(0)
	view := ansi.Strip(NewTaskDetailDialog("test", &model.Task{Name: "test", Cron: "0 3 * * *", Timeout: &zero}).View(80, 30))
	if strings.Contains(view, "Timeout") {
		t.Errorf("unexpected Timeout row for timeout = 0 in %q", view)
	}
}

func TestRetentionLabel(t *testing.T) {
	for d, want := range map[time.Duration]string{
		2880 * time.Hour: "120d",
		24 * time.Hour:   "1d",
		36 * time.Hour:   "36h",
	} {
		if got := retentionLabel(d); got != want {
			t.Errorf("retentionLabel(%v): want %q, got %q", d, want, got)
		}
	}
}

func TestTaskDetailDialog_ApplySummary_IgnoresOtherTask(t *testing.T) {
	d := NewTaskDetailDialog("alpha", &model.Task{Name: "alpha"})

	d.ApplySummary(uikit.TaskSummaryMsg{TaskName: "beta", Total: 5})
	if d.health.loaded {
		t.Fatal("summary for another task must be ignored")
	}

	d.ApplySummary(uikit.TaskSummaryMsg{TaskName: "alpha", Total: 5, Window: 1, Success: 1})
	if !d.health.loaded {
		t.Fatal("summary for this task must load")
	}
	if d.health.summary.Total != 5 {
		t.Fatalf("total: want 5, got %d", d.health.summary.Total)
	}
}

func TestTaskDetailDialog_View_RendersDefinitionAndHealth(t *testing.T) {
	d := NewTaskDetailDialog("backup-db", &model.Task{
		Name:          "backup-db",
		Kind:          model.KindTask,
		Cron:          "0 3 * * *",
		MaxConcurrent: 2,
		OnOverlap:     model.PolicyQueue,
		Parameters:    []model.TaskParam{{Kind: model.ParamEnv, Key: "TARGET"}},
	})

	// Before the async fetch resolves, the health block shows a loading hint.
	loadingView := d.View(80, 30)
	if !strings.Contains(loadingView, "backup-db") {
		t.Fatal("view should show the task name")
	}
	if !strings.Contains(loadingView, "Schedule") || !strings.Contains(loadingView, "0 3 * * *") {
		t.Fatal("view should show the schedule")
	}
	if !strings.Contains(loadingView, "Recent health") {
		t.Fatal("view should show the health section header")
	}
	if !strings.Contains(loadingView, "loading") {
		t.Fatal("view should show a loading hint before figures arrive")
	}

	d.ApplySummary(uikit.TaskSummaryMsg{TaskName: "backup-db", Total: 10, Window: 4, Success: 3, Failed: 1})
	healthView := d.View(80, 30)
	if !strings.Contains(healthView, "ok") || !strings.Contains(healthView, "failed") {
		t.Fatal("loaded view should show the ok/failed breakdown")
	}
	if !strings.Contains(healthView, "Success rate") {
		t.Fatal("loaded view should show the success rate")
	}
	if !strings.Contains(healthView, "TARGET") {
		t.Fatal("loaded view should list parameter keys")
	}
}

func TestTaskDetailDialog_View_ShowsDescription(t *testing.T) {
	d := NewTaskDetailDialog("test", &model.Task{Name: "test", Cron: "0 3 * * *", Description: "This is a test"})
	if !strings.Contains(d.View(80, 30), "This is a test") {
		t.Fatal("view should show the task description")
	}
}

func TestTaskDetailDialog_View_ClipsLongDescription(t *testing.T) {
	d := NewTaskDetailDialog("test", &model.Task{Name: "test", Description: strings.Repeat("word ", 80)})
	view := d.View(80, 30)
	if !strings.Contains(view, "…") {
		t.Fatal("a description past the line cap should end in an ellipsis")
	}
	if !strings.Contains(view, "Recent health") {
		t.Fatal("the health block should survive a long description")
	}
}

func TestTaskDetailDialog_View_ServiceWithAllFields(t *testing.T) {
	d := NewTaskDetailDialog("web", &model.Task{
		Name:          "web",
		Kind:          model.KindService,
		Group:         "frontend",
		Instances:     3,
		Restart:       model.RestartOnFailure,
		ManualTrigger: true,
		DependsOn:     []string{"db", "cache"},
		Compose:       &model.TaskComposeRef{File: "docker-compose.yml", Service: "web"},
		Parameters:    []model.TaskParam{{Kind: model.ParamEnv, Key: "PORT"}},
	})

	out := d.View(80, 40)
	for _, want := range []string{"service", "Instances", "Restart", "frontend", "Depends on", "db, cache", "Compose", "docker-compose.yml", "PORT"} {
		if !strings.Contains(out, want) {
			t.Fatalf("service view should contain %q", want)
		}
	}
	// "Manual trigger" is task-only vocabulary (Triggerable): a service is
	// never ad-hoc runnable regardless of ManualTrigger, so that label must
	// never surface here. A service's own use of the flag (locking manual
	// stop/restart/start) gets its own "Manual control" row instead; see
	// TestTaskDetailDialog_View_ServiceManualControlLocked.
	if strings.Contains(out, "Manual trigger") {
		t.Fatal("service view should not show Manual trigger: that label is task-only")
	}
	if strings.Contains(out, "Manual control") {
		t.Fatal("a manually-controllable service (the default) should not show a Manual control row")
	}
}

func TestTaskDetailDialog_View_ServiceManualControlLocked(t *testing.T) {
	d := NewTaskDetailDialog("web", &model.Task{
		Name:          "web",
		Kind:          model.KindService,
		Instances:     1,
		Restart:       model.RestartAlways,
		ManualTrigger: false,
	})

	out := d.View(80, 40)
	if !strings.Contains(out, "Manual control") || !strings.Contains(out, "locked") {
		t.Fatal("a manual_trigger=false service should show its manual control is locked")
	}
}

func TestTaskDetailDialog_View_StagedTaskNamesItsFileAndThePromoteCommand(t *testing.T) {
	d := NewTaskDetailDialog("backup", &model.Task{
		Name:   "backup",
		Kind:   model.KindTask,
		Cron:   "0 3 * * *",
		Source: model.SourceStaged,
	})

	out := d.View(80, 40)
	// Both halves must survive the value column's clipping — the command is the
	// half the operator acts on.
	for _, want := range []string{"Source", "runwisp.d/imported.toml", "Promote", "runwisp promote backup"} {
		if !strings.Contains(out, want) {
			t.Fatalf("staged view should contain %q, got:\n%s", want, out)
		}
	}

	native := NewTaskDetailDialog("backup", &model.Task{Name: "backup", Kind: model.KindTask})
	if strings.Contains(native.View(80, 40), "imported.toml") {
		t.Fatal("a task defined in the root config should not report a staging source")
	}
}

func TestTaskDetailDialog_View_HealthWithFailuresAndOther(t *testing.T) {
	d := NewTaskDetailDialog("backup", &model.Task{Name: "backup", Kind: model.KindTask})
	now := time.Now()
	d.ApplySummary(uikit.TaskSummaryMsg{
		TaskName:    "backup",
		Total:       50,
		Window:      10,
		Success:     6,
		Failed:      3,
		Other:       1,
		LastFailure: &now,
	})

	// A narrow width forces uikit.TruncateToWidth to truncate the wide breakdown row.
	out := d.View(40, 40)
	for _, want := range []string{"failed", "Success rate", "Last failure"} {
		if !strings.Contains(out, want) {
			t.Fatalf("loaded health view should contain %q", want)
		}
	}
}

func TestTaskDetailDialog_View_HealthError(t *testing.T) {
	d := NewTaskDetailDialog("x", &model.Task{Name: "x", Kind: model.KindTask})
	d.ApplySummary(uikit.TaskSummaryMsg{TaskName: "x", Err: errBoom()})
	out := d.View(60, 30)
	if !strings.Contains(out, "unavailable") {
		t.Fatal("a summary error should render an 'unavailable' health hint")
	}
}

func TestTaskDetailDialog_Update_ClosesOnKeys(t *testing.T) {
	for _, key := range []string{"i", "q"} {
		d := NewTaskDetailDialog("alpha", nil)
		if !closes(d, tea.KeyPressMsg{Code: []rune(key)[0], Text: key}) {
			t.Fatalf("rune %q should close the dialog", key)
		}
	}
	for _, kt := range []rune{tea.KeyEsc, tea.KeyEnter} {
		d := NewTaskDetailDialog("alpha", nil)
		if !closes(d, tea.KeyPressMsg{Code: kt}) {
			t.Fatalf("key %v should close the dialog", kt)
		}
	}
	d := NewTaskDetailDialog("alpha", nil)
	if closes(d, tea.KeyPressMsg{Code: 'x', Text: "x"}) {
		t.Fatal("unrelated key should not close the dialog")
	}
}

func TestHandleKeyI_OpensInspectorForCursorTask(t *testing.T) {
	m := newTestModel([]model.Task{{Name: "alpha"}})
	// Sidebar items: [Home(0), alpha(1), Info(2), Debug(3)] — put the cursor on alpha.
	selectSidebarItem(&m, 1)
	// The run command arrives with the /api/tasks poll, not /api/daemon.
	polled, _ := m.Update(uikit.TaskStateMsg{Commands: map[string]string{"alpha": "./alpha.sh"}})
	m = polled.(Model)

	updated, cmd := m.handleKey(tea.KeyPressMsg{Code: 'i', Text: "i"})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("handleKey did not return a Model")
	}
	if !got.dialogs.Has(dlgTaskDetail) {
		t.Fatal("pressing i with a task in focus should open the inspector")
	}
	if d, _ := got.dialogs.open[dlgTaskDetail].(*TaskDetailDialog); d == nil || d.command != "./alpha.sh" {
		t.Fatalf("the inspector should carry the polled run command, got %+v", d)
	}
	if cmd == nil {
		t.Fatal("opening the inspector should kick off the async health fetch")
	}
}

func TestHandleKeyI_FallsThroughWithoutTask(t *testing.T) {
	m := newTestModel(nil)
	// Default cursor is on Home — no task in focus.
	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'i', Text: "i"})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("handleKey did not return a Model")
	}
	if got.dialogs.Has(dlgTaskDetail) {
		t.Fatal("pressing i with no task in focus must not open the inspector")
	}
}

func TestTaskDetailDialog_View_LongLabelsKeepAGap(t *testing.T) {
	// "Manual trigger" is the longest label; it used to run straight into its
	// value ("Manual triggerenabled").
	d := NewTaskDetailDialog("alpha", &model.Task{Name: "alpha", Cron: "* * * * *", ManualTrigger: true})
	out := ansi.Strip(d.View(80, 40))
	if !strings.Contains(out, "Manual trigger  enabled") {
		t.Fatalf("expected a gap between label and value:\n%s", out)
	}
}
