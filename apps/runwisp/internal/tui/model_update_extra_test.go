// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
)

// TestUpdateHandlers_EarlyReturns covers the no-op branches of Update msg
// handlers: nil-run, nil-err, not-viewing-run, and the LoadingOlder-already
// guard on maybeLoadOlderLogs.
func TestUpdateHandlers_EarlyReturns(t *testing.T) {
	t.Run("handleOpenRun with nil run returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if _, cmd := m.handleOpenRun(uikit.OpenRunMsg{Run: nil}); cmd != nil {
			t.Fatal("expected nil cmd for nil run")
		}
	})

	t.Run("handleNotificationReadState with nil err returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		_, cmd := m.handleNotificationReadState(uikit.NotificationReadStateMsg{ID: "n1", Read: true})
		if cmd != nil {
			t.Fatal("expected nil cmd when no error")
		}
	})

	t.Run("handleLogOlderLoaded with no matching run returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if _, cmd := m.handleLogOlderLoaded(uikit.LogOlderLoadedMsg{RunID: "run-123"}); cmd != nil {
			t.Fatal("expected nil cmd when not viewing run")
		}
	})

	t.Run("viewingRun is false when execView is nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.viewingRun("run-123") {
			t.Fatal("expected false when execView is nil")
		}
	})

	t.Run("maybeLoadOlderLogs is nil when execView is nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.maybeLoadOlderLogs() != nil {
			t.Fatal("expected nil cmd when execView is nil")
		}
	})

	t.Run("maybeLoadOlderLogs is nil when LoadingOlder is already true", func(t *testing.T) {
		m := newTestModel(nil)
		run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
		ev := execlist.NewExecView(run)
		m.execView = &ev
		m.execView.LoadingOlder = true
		if m.maybeLoadOlderLogs() != nil {
			t.Fatal("expected nil cmd when LoadingOlder is true")
		}
	})
}

// ─── interceptCopyDialog ─────────────────────────────────────────────────────

func TestInterceptCopyDialog_CtrlCDismissesAndShowsQuit(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowCopy("title", "value")
	if !m.dialogs.HasCopy() {
		t.Fatal("expected copy dialog to be active")
	}

	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	newModelIface, _, intercepted := m.interceptCopyDialog(msg)
	if !intercepted {
		t.Fatal("expected intercepted=true for ctrl+c")
	}
	newM := newModelIface.(Model)
	if newM.dialogs.HasCopy() {
		t.Fatal("expected copy dialog dismissed after ctrl+c")
	}
}

func TestInterceptCopyDialog_AnyKeyWhileVisibleIsIntercepted(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowCopy("title", "value")

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
	_, _, intercepted := m.interceptCopyDialog(msg)
	if !intercepted {
		t.Fatal("expected intercepted=true for any key while copy dialog is visible")
	}
}

// ─── handleOpenBrowser ───────────────────────────────────────────────────────

func TestHandleOpenBrowser_BrowserOpenedReturnsFlashCmd(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleOpenBrowser(uikit.OpenBrowserMsg{
		URL:           "http://localhost:8080/launch?ticket=abc",
		BrowserOpened: true,
	})
	if cmd == nil {
		t.Fatal("expected non-nil cmd (flash) when browser was opened")
	}
}

// ─── viewingRun ───────────────────────────────────────────────────────────────

func TestViewingRun_WithMatchingExecView(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "run-123", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	if !m.viewingRun("run-123") {
		t.Fatal("expected true when execView has the given runID")
	}
}

func TestViewingRun_WithNonMatchingExecView(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "run-456", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	if m.viewingRun("run-123") {
		t.Fatal("expected false when execView has different runID")
	}
}

// ─── handleDaemonInfo ────────────────────────────────────────────────────────

func TestHandleDaemonInfo_UpdatesConfigStale(t *testing.T) {
	m := newTestModel(nil)
	updated, cmd := m.handleDaemonInfo(uikit.DaemonInfoMsg{
		Info: &model.DaemonInfo{ConfigStale: true, ServiceManaged: true},
	})
	if cmd != nil {
		t.Fatal("expected nil cmd")
	}
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if !got.info.ConfigStale {
		t.Fatal("expected ConfigStale to be set from daemon info")
	}
	if !got.info.ServiceManaged {
		t.Fatal("expected ServiceManaged to be set from daemon info")
	}
}

func TestHandleDaemonInfo_ErrorKeepsLastKnownState(t *testing.T) {
	m := newTestModel(nil)
	m.info.ConfigStale = true
	updated, _ := m.handleDaemonInfo(uikit.DaemonInfoMsg{Err: errors.New("connection refused")})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if !got.info.ConfigStale {
		t.Fatal("expected error to leave ConfigStale untouched")
	}
}

// ─── handleQuit ───────────────────────────────────────────────────────────────

func TestHandleQuit_KeepDaemon(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleQuit(uikit.QuitMsg{Action: uikit.QuitKeepDaemon})
	if cmd == nil {
		t.Fatal("expected non-nil cmd (tea.Quit) for KeepDaemon")
	}
}

func TestHandleQuit_ShutdownDaemonNoShutdownFunc(t *testing.T) {
	m := newTestModel(nil)
	m.shutdownFunc = nil
	_, cmd := m.handleQuit(uikit.QuitMsg{Action: uikit.QuitShutdownDaemon})
	if cmd == nil {
		t.Fatal("expected non-nil cmd (tea.Quit) when shutdownFunc is nil")
	}
}

// ─── interceptConfirmDialog ──────────────────────────────────────────────────

func TestInterceptConfirmDialog_CtrlC(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))

	_, _, intercepted := m.interceptConfirmDialog(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !intercepted {
		t.Fatal("expected intercepted=true for ctrl+c")
	}
}

func TestInterceptConfirmDialog_MouseMsg(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))

	_, _, intercepted := m.interceptConfirmDialog(tea.MouseMsg{})
	if !intercepted {
		t.Fatal("expected intercepted=true for mouse msg with confirm dialog")
	}
}

func TestInterceptConfirmDialog_OtherMsg(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))

	_, _, intercepted := m.interceptConfirmDialog(uikit.TickMsg{})
	if intercepted {
		t.Fatal("expected intercepted=false for non-key/mouse msg")
	}
}

// ─── interceptShuttingDownDialog ─────────────────────────────────────────────

func TestInterceptShuttingDownDialog_CtrlC(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	_, _, intercepted := m.interceptShuttingDownDialog(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !intercepted {
		t.Fatal("expected intercepted=true for ctrl+c in shutdown dialog")
	}
}

func TestInterceptShuttingDownDialog_ShutdownDone(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	_, _, intercepted := m.interceptShuttingDownDialog(uikit.ShutdownDoneMsg{})
	if !intercepted {
		t.Fatal("expected intercepted=true for ShutdownDoneMsg")
	}
}

func TestInterceptShuttingDownDialog_OtherKey(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	_, _, intercepted := m.interceptShuttingDownDialog(tea.KeyMsg{Type: tea.KeyEnter})
	if !intercepted {
		t.Fatal("expected intercepted=true for any key in shutdown dialog")
	}
}
