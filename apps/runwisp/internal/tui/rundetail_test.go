// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	_ "time/tzdata"
)

func endedRun() *model.Run {
	parent := "01HZRUNPARENTAAAAAAAAAAAAAA"
	return &model.Run{
		ID:           "01HZRUNCHILDBBBBBBBBBBBBBBB",
		TaskName:     "backup-db",
		Status:       model.PhaseEnded,
		EndReason:    model.EndReasonPtr(model.ReasonFailed),
		ExitCode:     7,
		TriggeredBy:  model.TriggeredByCron,
		RetryAttempt: 1,
		RetryOfRunID: &parent,
	}
}

func TestRunDetailDialog_View_RendersFacts(t *testing.T) {
	d := NewRunDetailDialog(endedRun(), false, 1)
	out := d.View(80, 30)

	for _, want := range []string{"backup-db", "Status", "failed", "Exit code", "7", "Triggered", "cron", "Retry of", "01HZRUNPARENT", "enter open parent"} {
		if !strings.Contains(out, want) {
			t.Fatalf("run detail view missing %q\n%s", want, out)
		}
	}
}

func TestRunDetailDialog_View_ResourceUsage(t *testing.T) {
	run := endedRun()
	d := NewRunDetailDialog(run, false, 1)
	if out := d.View(80, 30); strings.Contains(out, "Peak memory") {
		t.Fatal("an unmeasured run should not show resource rows")
	}
	peak, cpu := int64(48<<20), int64(1250)
	run.PeakMemoryBytes, run.CPUTimeMs = &peak, &cpu
	d = NewRunDetailDialog(run, false, 1)
	out := d.View(80, 34)
	for _, want := range []string{"Peak memory", "48 MB", "CPU time", "1.25s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("run detail view missing %q\n%s", want, out)
		}
	}
}

func TestRunDetailDialog_View_FirstAttemptHasNoLineage(t *testing.T) {
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning, TriggeredBy: model.TriggeredByAPI}
	d := NewRunDetailDialog(run, false, 1)
	out := d.View(80, 30)

	if strings.Contains(out, "Retry of") {
		t.Fatal("a first-attempt run should not show retry lineage")
	}
	if strings.Contains(out, "Exit code") {
		t.Fatal("a still-running run should not show an exit code")
	}
	if !strings.Contains(out, "esc close") || strings.Contains(out, "open parent") {
		t.Fatal("a run with no parent should offer only the plain close hint")
	}
}

func TestRunDetailDialog_View_SuccessServiceInstance(t *testing.T) {
	end := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	start := end.Add(-time.Minute)
	run := &model.Run{
		ID:            "01HZRUNOKAAAAAAAAAAAAAAAAAA",
		TaskName:      "web",
		Status:        model.PhaseEnded,
		EndReason:     model.EndReasonPtr(model.ReasonSuccess),
		ExitCode:      0,
		TriggeredBy:   model.TriggeredByAPI,
		InstanceIndex: 1,
		StartedAt:     &start,
		EndedAt:       &end,
		CreatedAt:     start,
		Params:        map[string]string{"PORT": "8080"},
	}
	d := NewRunDetailDialog(run, true, 3)
	out := d.View(80, 30)

	for _, want := range []string{"web #2", "succeeded", "Exit code", "0", "Instance", "Started", "Ended", "Params"} {
		if !strings.Contains(out, want) {
			t.Fatalf("success service run view missing %q\n%s", want, out)
		}
	}
}

func TestRunDetailDialog_ParentRef(t *testing.T) {
	d := NewRunDetailDialog(endedRun(), false, 1)
	taskName, runID, ok := d.ParentRef()
	if !ok || taskName != "backup-db" || runID != "01HZRUNPARENTAAAAAAAAAAAAAA" {
		t.Fatalf("parent ref: got (%q,%q,%v)", taskName, runID, ok)
	}

	noParent := NewRunDetailDialog(&model.Run{ID: "r1", TaskName: "t1"}, false, 1)
	if _, _, ok := noParent.ParentRef(); ok {
		t.Fatal("a non-retry run must report no parent")
	}
}

func TestRunDetailDialog_Update_CloseKeys(t *testing.T) {
	for _, key := range []string{"i", "q"} {
		d := NewRunDetailDialog(endedRun(), false, 1)
		if !closes(d, tea.KeyPressMsg{Code: []rune(key)[0], Text: key}) {
			t.Fatalf("rune %q should close the dialog", key)
		}
	}
	d := NewRunDetailDialog(endedRun(), false, 1)
	if !closes(d, tea.KeyPressMsg{Code: tea.KeyEsc}) {
		t.Fatal("esc should close the dialog")
	}
	// Enter is reserved for the interceptor (open parent), so the dialog itself
	// must not treat it as a close.
	enterDialog := NewRunDetailDialog(endedRun(), false, 1)
	if closes(enterDialog, tea.KeyPressMsg{Code: tea.KeyEnter}) {
		t.Fatal("enter must not close the dialog directly")
	}
}

func TestHandleKeyI_ExecViewOpensRunDetail(t *testing.T) {
	m := newTestModel([]model.Task{{Name: "backup-db"}})
	ev := execlist.NewExecView(endedRun())
	m.execView = &ev

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'i', Text: "i"})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("handleKey did not return a Model")
	}
	if !got.dialogs.Has(dlgRunDetail) {
		t.Fatal("pressing i in an exec view should open the run inspector")
	}
	if got.dialogs.Has(dlgTaskDetail) {
		t.Fatal("the exec view inspects the run, not the task")
	}
}

func TestInterceptRunDetail_EnterOpensParent(t *testing.T) {
	m := newTestModel([]model.Task{{Name: "backup-db"}})
	m.dialogs.Show(dlgRunDetail, NewRunDetailDialog(endedRun(), false, 1))

	updated, cmd, intercepted := m.interceptActiveDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !intercepted {
		t.Fatal("run inspector should intercept enter")
	}
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("interceptor did not return a Model")
	}
	if got.dialogs.Has(dlgRunDetail) {
		t.Fatal("opening the parent should dismiss the inspector")
	}
	if cmd == nil {
		t.Fatal("opening the parent should produce a fetch command")
	}
}

// The TUI can run in a different zone than the daemon (a container, a service
// user without TZ). Timestamps must follow the daemon's zone.
func TestRunDetailDialog_ShowsTimesInDaemonZone(t *testing.T) {
	run := endedRun()
	run.CreatedAt = time.Date(2026, 10, 4, 1, 15, 0, 0, time.UTC)
	m := NewModel(TUIConfig{
		Client: newDummyClient(),
		Info:   uikit.StartupInfo{Version: "0.0.0-test", Timezone: "Etc/GMT-2"}, // UTC+2
	})
	ev := execlist.NewExecView(run)
	m.execView = &ev

	updated, _ := m.handleKey(tea.KeyPressMsg{Code: 'i', Text: "i"})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("handleKey did not return a Model")
	}
	out := got.dialogs.open[dlgRunDetail].View(80, 30)
	if !strings.Contains(out, "2026-10-04 03:15:00") {
		t.Fatalf("started time should be shown in the daemon's zone (03:15), got:\n%s", out)
	}
}

func TestHandleDaemonInfo_AdoptsTimezoneChange(t *testing.T) {
	m := newTestModel(nil)
	updated, _ := m.handleDaemonInfo(uikit.DaemonInfoMsg{Info: &model.DaemonInfo{ResolvedTimezone: "Etc/GMT-2", TimezoneSource: "config"}})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("handleDaemonInfo did not return a Model")
	}
	if got.loc.String() != "Etc/GMT-2" {
		t.Fatalf("loc: want Etc/GMT-2, got %v", got.loc)
	}
}
