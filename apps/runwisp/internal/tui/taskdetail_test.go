// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

func TestSummarizeTaskRuns_ClassifiesAndPicksLatestFailure(t *testing.T) {
	newest := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	older := newest.Add(-time.Hour)

	// Newest-first, as the fetch requests.
	runs := []model.Run{
		{Status: model.PhaseRunning}, // other
		{Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonCrashed), EndAt: &newest}, // failure (latest)
		{Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess)},                 // success
		{Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonFailed), EndAt: &older},   // failure
		{Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSkipped)},                 // other
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

func TestTaskDetailDialog_ApplySummary_IgnoresOtherTask(t *testing.T) {
	d := NewTaskDetailDialog("alpha", &model.TaskBrief{Name: "alpha"})

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
	d := NewTaskDetailDialog("backup-db", &model.TaskBrief{
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

func TestTaskDetailDialog_View_ServiceWithAllFields(t *testing.T) {
	d := NewTaskDetailDialog("web", &model.TaskBrief{
		Name:       "web",
		Kind:       model.KindService,
		Group:      "frontend",
		Instances:  3,
		Restart:    model.RestartOnFailure,
		APITrigger: true,
		DependsOn:  []string{"db", "cache"},
		Compose:    &model.TaskComposeRef{File: "docker-compose.yml", Service: "web"},
		Parameters: []model.TaskParam{{Kind: model.ParamEnv, Key: "PORT"}},
	})

	out := d.View(80, 40)
	for _, want := range []string{"service", "Instances", "Restart", "frontend", "API trigger", "Depends on", "db, cache", "Compose", "docker-compose.yml", "PORT"} {
		if !strings.Contains(out, want) {
			t.Fatalf("service view should contain %q", want)
		}
	}
}

func TestTaskDetailDialog_View_HealthWithFailuresAndOther(t *testing.T) {
	d := NewTaskDetailDialog("backup", &model.TaskBrief{Name: "backup", Kind: model.KindTask})
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

	// A narrow width forces clipToWidth to truncate the wide breakdown row.
	out := d.View(40, 40)
	for _, want := range []string{"failed", "Success rate", "Last failure"} {
		if !strings.Contains(out, want) {
			t.Fatalf("loaded health view should contain %q", want)
		}
	}
}

func TestTaskDetailDialog_View_HealthError(t *testing.T) {
	d := NewTaskDetailDialog("x", &model.TaskBrief{Name: "x", Kind: model.KindTask})
	d.ApplySummary(uikit.TaskSummaryMsg{TaskName: "x", Err: errBoom()})
	out := d.View(60, 30)
	if !strings.Contains(out, "unavailable") {
		t.Fatal("a summary error should render an 'unavailable' health hint")
	}
}

func TestTaskDetailDialog_Update_ClosesOnKeys(t *testing.T) {
	for _, key := range []string{"i", "q"} {
		d := NewTaskDetailDialog("alpha", nil)
		if !d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}) {
			t.Fatalf("rune %q should close the dialog", key)
		}
	}
	for _, kt := range []tea.KeyType{tea.KeyEsc, tea.KeyEnter} {
		d := NewTaskDetailDialog("alpha", nil)
		if !d.Update(tea.KeyMsg{Type: kt}) {
			t.Fatalf("key %v should close the dialog", kt)
		}
	}
	d := NewTaskDetailDialog("alpha", nil)
	if d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) {
		t.Fatal("unrelated key should not close the dialog")
	}
}

func TestHandleKeyI_OpensInspectorForCursorTask(t *testing.T) {
	m := newTestModelWithClient([]model.TaskBrief{{Name: "alpha"}})
	// Sidebar items: [Home(0), alpha(1), Info(2), Debug(3)] — put the cursor on alpha.
	selectSidebarItem(&m, 1)

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("handleKey did not return a Model")
	}
	if !got.dialogs.HasTaskDetail() {
		t.Fatal("pressing i with a task in focus should open the inspector")
	}
	if cmd == nil {
		t.Fatal("opening the inspector should kick off the async health fetch")
	}
}

func TestHandleKeyI_FallsThroughWithoutTask(t *testing.T) {
	m := newTestModelWithClient(nil)
	// Default cursor is on Home — no task in focus.
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("handleKey did not return a Model")
	}
	if got.dialogs.HasTaskDetail() {
		t.Fatal("pressing i with no task in focus must not open the inspector")
	}
}
