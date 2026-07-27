// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
)

func errBoom() error { return errors.New("boom") }

// TestModel_Guards covers the early-return / no-op branches of helpers that
// short-circuit on empty selections, nil execView, nil clients, or out-of-range
// cursors. These exist as individual blocks in Go's coverage profile, so the
// table preserves block coverage while removing per-case boilerplate.
func TestModel_Guards(t *testing.T) {
	t.Run("toggleSelectedNotificationRead returns nil with no selection", func(t *testing.T) {
		m := newTestModel(nil)
		if m.toggleSelectedNotificationRead() != nil {
			t.Fatal("expected nil cmd when no selection")
		}
	})

	t.Run("closeExecView is safe with nil execView", func(t *testing.T) {
		m := newTestModel(nil)
		// must not panic; syncMouseState may return nil
		m.closeExecView()
	})

	t.Run("syncMouseState first call with no holds returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if cmd := m.syncMouseState(); cmd != nil {
			t.Fatalf("expected nil cmd from baseline syncMouseState, got %v", cmd)
		}
	})

	t.Run("latestRunningExec on empty window returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.latestRunningExec("t1") != nil {
			t.Fatal("expected nil for empty window")
		}
	})

	t.Run("copyExecField with nil execView returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.copyExecField() != nil {
			t.Fatal("expected nil cmd when no execView")
		}
	})

	t.Run("openRunByID with nil client and run not in window returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.openRunByID("task", "run-id-123") != nil {
			t.Fatal("expected nil cmd when client is nil and run not in window")
		}
	})

	t.Run("downloadExecLog with nil execView returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.downloadExecLog() != nil {
			t.Fatal("expected nil when no execView")
		}
	})

	t.Run("openLaunchURL with nil launchTicketFunc returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.openLaunchURL("/") != nil {
			t.Fatal("expected nil when launchTicketFunc is nil")
		}
	})

	t.Run("openWebUI with nil launchTicketFunc returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.openWebUI() != nil {
			t.Fatal("expected nil when launchTicketFunc is nil")
		}
	})

	t.Run("activateHomeField with negative cursor returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		m.homeCursor = -1
		if m.activateHomeField() != nil {
			t.Fatal("expected nil for negative cursor")
		}
	})

	t.Run("activateHomeField with cursor beyond fields returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		// No fields (port=0, no password) → len(fields)==0
		m.homeCursor = 5
		if m.activateHomeField() != nil {
			t.Fatal("expected nil when cursor beyond fields length")
		}
	})
}

// TestModel_Init_ReturnsBatchedCmd makes sure Init produces a non-nil command
// — the dashboard's initial subscriptions need to fire.
func TestModel_Init_ReturnsBatchedCmd(t *testing.T) {
	m := newTestModel(nil)
	if m.Init() == nil {
		t.Fatal("Init must produce a tea.Cmd to bootstrap the TUI")
	}
}

func TestModel_QuitAction_DefaultsToKeepDaemon(t *testing.T) {
	m := newTestModel(nil)
	if got := m.QuitAction(); got != uikit.QuitKeepDaemon {
		t.Fatalf("expected QuitKeepDaemon (default zero value) for fresh model, got %v", got)
	}
}

func TestModel_QuitAction_ReflectsAssignment(t *testing.T) {
	m := newTestModel(nil)
	m.quitAction = uikit.QuitShutdownDaemon
	if got := m.QuitAction(); got != uikit.QuitShutdownDaemon {
		t.Fatalf("QuitAction must return the stored action, got %v", got)
	}
}

func TestModel_OpenLaunchURL_NonNilCmd(t *testing.T) {
	m := newTestModel(nil)
	m.launchTicketFunc = func() (string, error) { return "tkt-1", nil }
	if m.openLaunchURL("/some/path") == nil {
		t.Fatal("openLaunchURL must produce a tea.Cmd when launchTicketFunc is set")
	}
	if m.openLaunchURL("") == nil {
		t.Fatal("openLaunchURL must default empty target to /")
	}
}

func TestModel_OpenWebUI_NonNilCmd(t *testing.T) {
	m := newTestModel(nil)
	m.launchTicketFunc = func() (string, error) { return "tkt-1", nil }
	if m.openWebUI() == nil {
		t.Fatal("openWebUI must produce a tea.Cmd when launchTicketFunc is set")
	}
}

func TestModel_OpenLaunchURL_TicketSuccess_ReturnsURLMsg(t *testing.T) {
	// Force canOpenBrowser() to return false so cmd() takes the clipboard
	// fallback branch instead of actually launching xdg-open on dev machines
	// (or `open` on macOS, which ignores DISPLAY/WAYLAND_DISPLAY).
	stubCanOpenBrowser(t, false)

	m := newTestModel(nil)
	m.info.Port = 8181
	m.launchTicketFunc = func() (string, error) { return "tkt-xyz", nil }

	cmd := m.openLaunchURL("/path/foo")
	if cmd == nil {
		t.Fatal("expected cmd from openLaunchURL")
	}
	got := cmd()
	bMsg, ok := got.(uikit.OpenBrowserMsg)
	if !ok {
		t.Fatalf("got %T, want uikit.OpenBrowserMsg", got)
	}
	if want := "http://localhost:8181/api/auth/launch?ticket=tkt-xyz&redirect=%2Fpath%2Ffoo"; bMsg.URL != want {
		t.Fatalf("URL = %q, want %q", bMsg.URL, want)
	}
	if bMsg.BrowserOpened {
		t.Fatal("BrowserOpened must be false when canOpenBrowser is false")
	}
}

func TestModel_OpenLaunchURL_TicketError_ReturnsErrMsg(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	m := newTestModel(nil)
	m.launchTicketFunc = func() (string, error) { return "", errBoom() }
	cmd := m.openLaunchURL("/")
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	got := cmd()
	bMsg, ok := got.(uikit.OpenBrowserMsg)
	if !ok {
		t.Fatalf("got %T, want uikit.OpenBrowserMsg", got)
	}
	if bMsg.Err == nil {
		t.Fatal("expected non-nil err on ticket failure")
	}
}

func TestModel_DownloadExecLog_HappyPath(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	m := newTestModel(nil)
	m.info.Port = 9090
	m.launchTicketFunc = func() (string, error) { return "tkt-dl", nil }

	run := &model.Run{ID: "r-1", TaskName: "task-A"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	cmd := m.downloadExecLog()
	if cmd == nil {
		t.Fatal("expected non-nil downloadExecLog cmd")
	}
	msg := cmd()
	bMsg, ok := msg.(uikit.OpenBrowserMsg)
	if !ok {
		t.Fatalf("got %T, want OpenBrowserMsg", msg)
	}
	// The redirect param is URL-encoded; check the encoded form.
	if !strings.Contains(bMsg.URL, "%2Fapi%2Ftasks%2Ftask-A%2Fruns%2Fr-1%2Flog%2Fraw") {
		t.Fatalf("URL missing raw-log target: %q", bMsg.URL)
	}
}

func TestModel_DownloadExecLog_RunMissingTaskOrIDReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	m.launchTicketFunc = func() (string, error) { return "ok", nil }

	run := &model.Run{ID: "", TaskName: ""}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	if m.downloadExecLog() != nil {
		t.Fatal("expected nil for empty TaskName/ID")
	}
}

func TestModel_ActivateHomeField_OutOfRangeCursor(t *testing.T) {
	m := newTestModel(nil)
	m.homeCursor = 999 // out of range
	if m.activateHomeField() != nil {
		t.Fatal("expected nil for out-of-range cursor")
	}
	m.homeCursor = -1
	if m.activateHomeField() != nil {
		t.Fatal("expected nil for negative cursor")
	}
}

func TestModel_TickCmd_ReturnsTickMsg(t *testing.T) {
	m := newTestModel(nil)
	cmd := m.tickCmd()
	if cmd == nil {
		t.Fatal("tickCmd must produce a cmd")
	}
	// tea.Tick produces an internal-blocking command; we can confirm the
	// command type is non-nil and producible. Invoking and waiting takes
	// tickInterval real time — we don't wait, just verify shape.
	_ = cmd
}

// ─── closeExecView ────────────────────────────────────────────────────────────

func TestCloseExecView_SetsExecListFocusedWhenPanelMain(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	m.panelFocus = uikit.PanelMain

	m.closeExecView()
	if m.execView != nil {
		t.Fatal("expected execView to be nil after closeExecView")
	}
}

// ─── serviceInstances ────────────────────────────────────────────────────────

func TestServiceInstances_UnknownTaskReturns0(t *testing.T) {
	m := newTestModel(nil)
	if got := m.serviceInstances("unknown"); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestServiceInstances_ServiceWithZeroInstances(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "svc", Kind: model.KindService, Instances: 0},
	}
	m := newTestModel(tasks)
	// Instances < 1 → returns 1
	if got := m.serviceInstances("svc"); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

func TestServiceInstances_ServiceWithThreeInstances(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "svc", Kind: model.KindService, Instances: 3},
	}
	m := newTestModel(tasks)
	if got := m.serviceInstances("svc"); got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
}

func TestServiceInstances_NonServiceTask(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "job", Kind: model.KindTask},
	}
	m := newTestModel(tasks)
	// not a service → 0
	if got := m.serviceInstances("job"); got != 0 {
		t.Fatalf("expected 0 for non-service, got %d", got)
	}
}

// ─── autoOpenService (negative branches) ─────────────────────────────────────

func TestAutoOpenService_NonServiceTaskReturnsNil(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "job", Kind: model.KindTask},
	}
	m := newTestModel(tasks)
	if m.autoOpenService("job") != nil {
		t.Fatal("expected nil for non-service task")
	}
}

func TestAutoOpenService_UnknownTaskReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	if m.autoOpenService("unknown") != nil {
		t.Fatal("expected nil for unknown task")
	}
}

// ─── markRunNotificationsRead ────────────────────────────────────────────────

func TestMarkRunNotificationsRead_MarksLocallyRead(t *testing.T) {
	m := newTestModel(nil)
	n := server.NotificationDTO{
		ID:             "n1",
		Severity:       "info",
		Count:          1,
		LastOccurredAt: time.Now(),
		Title:          "test",
		RunID:          "run-999",
	}
	m.notifications.Upsert(n)

	if len(m.notifications.UnreadIDsForRun("run-999")) == 0 {
		t.Fatal("precondition: expected at least one unread notification for run-999")
	}

	_ = m.markRunNotificationsRead("run-999")

	if got := len(m.notifications.UnreadIDsForRun("run-999")); got != 0 {
		t.Fatalf("expected notification to be marked read locally, still %d unread", got)
	}
}

// ─── autoOpenService ─────────────────────────────────────────────────────────

func TestAutoOpenService_SingleInstanceWithRunningExecOpensView(t *testing.T) {
	tasks := []model.TaskBrief{{
		Name:          "svc",
		Kind:          model.KindService,
		MaxConcurrent: 1,
	}}
	m := newTestModel(tasks)

	run := model.Run{
		ID:       "r-svc",
		TaskName: "svc",
		Status:   model.PhaseRunning,
	}
	m.execWindow.UpsertRun(run)

	_ = m.autoOpenService("svc")
	if m.execView == nil {
		t.Fatal("expected execView to be set after autoOpenService with running exec")
	}
	if m.execView.Run == nil || m.execView.Run.ID != "r-svc" {
		t.Fatalf("expected execView to wrap run r-svc, got %+v", m.execView.Run)
	}
}

// ─── openRunByID ─────────────────────────────────────────────────────────────

func TestOpenRunByID_RunInWindowOpensExecView(t *testing.T) {
	m := newTestModel(nil)
	run := model.Run{
		ID:       "r-found",
		TaskName: "backup-db",
		Status:   model.PhasePending,
	}
	m.execWindow.UpsertRun(run)

	_ = m.openRunByID("backup-db", "r-found")
	if m.execView == nil {
		t.Fatal("expected execView to be set when run is found in window")
	}
	if m.execView.Run == nil || m.execView.Run.ID != "r-found" {
		t.Fatalf("expected execView to wrap run r-found, got %+v", m.execView.Run)
	}
}

// ─── resolveTaskName ─────────────────────────────────────────────────────────

func TestResolveTaskName_PanelMainReturnsSidebarActive(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup-db"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 1)
	m.focusMainPanel()

	if name := m.resolveTaskName(); name != "backup-db" {
		t.Fatalf("expected backup-db, got %q", name)
	}
}

// ─── showQuitConfirm ─────────────────────────────────────────────────────────

func TestShowQuitConfirm_OpensConfirmDialog(t *testing.T) {
	m := newTestModel(nil)
	if m.dialogs.HasConfirm() {
		t.Fatal("no dialog expected initially")
	}
	m.showQuitConfirm()
	if !m.dialogs.HasConfirm() {
		t.Fatal("expected confirm dialog after showQuitConfirm")
	}
}

// A service-managed daemon must not be shut down from the TUI — the dialog
// drops the "Shut Down" action and points at `runwisp stop` instead.
func TestShowQuitConfirm_ServiceManagedDropsShutdownOption(t *testing.T) {
	m := newTestModel(nil)
	m.info.ServiceManaged = true
	m.showQuitConfirm()

	d := m.dialogs.confirmDialog
	if d == nil {
		t.Fatal("expected confirm dialog after showQuitConfirm")
	}
	if d.yesLabel != "Quit TUI" {
		t.Fatalf("expected Quit TUI option, got %q", d.yesLabel)
	}
	if d.onDeny != nil {
		t.Fatal("service-managed quit dialog must not offer a shutdown action")
	}
	note := strings.Join(d.noteLines, "\n")
	if !strings.Contains(note, "runwisp stop") {
		t.Fatalf("expected `runwisp stop` hint in note, got %q", note)
	}
}
