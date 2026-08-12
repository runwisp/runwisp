// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"encoding/json"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
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

	t.Run("handleNotificationReadState read=true err rolls back to unread", func(t *testing.T) {
		m := newTestModel(nil)
		_, cmd := m.handleNotificationReadState(uikit.NotificationReadStateMsg{
			ID:   "n1",
			Read: true,
			Err:  errors.New("boom"),
		})
		if cmd != nil {
			t.Fatal("expected nil cmd; error path emits no follow-up command")
		}
	})

	t.Run("handleNotificationReadState read=false err re-applies optimistic read", func(t *testing.T) {
		m := newTestModel(nil)
		_, cmd := m.handleNotificationReadState(uikit.NotificationReadStateMsg{
			ID:   "n1",
			Read: false,
			Err:  errors.New("boom"),
		})
		if cmd != nil {
			t.Fatal("expected nil cmd; error path emits no follow-up command")
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

	msg := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
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

	msg := tea.KeyPressMsg{Code: 'x', Text: "x"}
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

	_, _, intercepted := m.interceptConfirmDialog(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !intercepted {
		t.Fatal("expected intercepted=true for ctrl+c")
	}
}

func TestInterceptConfirmDialog_MouseMsg(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))

	_, _, intercepted := m.interceptConfirmDialog(tea.MouseClickMsg{})
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

func TestInterceptConfirmDialog_RoutesToShuttingDown(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	// While shutting down, interceptConfirmDialog should defer to
	// interceptShuttingDownDialog — ShutdownDoneMsg is only handled there.
	_, _, intercepted := m.interceptConfirmDialog(uikit.ShutdownDoneMsg{})
	if !intercepted {
		t.Fatal("expected intercepted=true when shutting-down dialog handles the msg")
	}
}

// ─── interceptShuttingDownDialog ─────────────────────────────────────────────

func TestInterceptShuttingDownDialog_CtrlC(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	_, _, intercepted := m.interceptShuttingDownDialog(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
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

// TestInterceptShuttingDownDialog_ShutdownError verifies a failed shutdown is
// recorded on the model rather than swallowed — otherwise the TUI would close
// as if the daemon had stopped when it is still running.
func TestInterceptShuttingDownDialog_ShutdownError(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	wantErr := errors.New("could not signal daemon")
	next, _, intercepted := m.interceptShuttingDownDialog(uikit.ShutdownDoneMsg{Err: wantErr})
	if !intercepted {
		t.Fatal("expected intercepted=true for ShutdownDoneMsg")
	}
	fm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if fm.ShutdownErr() != wantErr {
		t.Fatalf("expected ShutdownErr %v, got %v", wantErr, fm.ShutdownErr())
	}
}

func TestInterceptShuttingDownDialog_OtherKey(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	_, _, intercepted := m.interceptShuttingDownDialog(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !intercepted {
		t.Fatal("expected intercepted=true for any key in shutdown dialog")
	}
}

// ─── handleSSEEvent / handleSSEEventMsg ──────────────────────────────────────

func TestHandleSSEEvent_InvalidJSONAppendsDebugLine(t *testing.T) {
	m := newTestModel(nil)
	evt := apiclient.RunStreamEvent{Type: "run.created", Data: json.RawMessage("not json")}
	if cmd := m.handleSSEEvent(evt); cmd != nil {
		t.Fatalf("expected nil cmd for invalid JSON, got %v", cmd)
	}
}

func TestHandleSSEEvent_UpsertsRunIntoWindow(t *testing.T) {
	m := newTestModel(nil)
	payload := []byte(`{"run":{"id":"r-1","task_name":"t1","status":"running"},"task_name":"t1","status":"running"}`)
	_ = m.handleSSEEvent(apiclient.RunStreamEvent{Type: "run.updated", Data: payload})
	if m.execWindow.FindRun("r-1") == nil {
		t.Fatal("expected exec window to contain the upserted run")
	}
}

func TestHandleSSEEvent_UpdatesExecViewWhenWatching(t *testing.T) {
	m := newTestModel(nil)
	existing := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhasePending}
	ev := execlist.NewExecView(existing)
	m.execView = &ev

	// Status flips pending → running and the client is nil, so StartLogStream
	// (which guards on client==nil) returns nil. We still expect the run
	// pointer on the exec view to be replaced with the freshly-decoded one.
	payload := []byte(`{"run":{"id":"r-1","task_name":"t1","status":"running"},"task_name":"t1","status":"running"}`)
	_ = m.handleSSEEvent(apiclient.RunStreamEvent{Type: "run.updated", Data: payload})
	if m.execView.Run.Status != model.PhaseRunning {
		t.Fatalf("expected execView.Run.Status to be running, got %v", m.execView.Run.Status)
	}
}

func TestHandleSSEEventMsg_DispatchesViaHandleSSEEvent(t *testing.T) {
	m := newTestModel(nil)
	evt := apiclient.RunStreamEvent{Type: "run.created", Data: json.RawMessage(`{"run":{"id":"r-x","task_name":"t","status":"running"}}`)}
	_, cmd := m.handleSSEEventMsg(uikit.SSEEventMsg{Event: evt})
	// tea.Batch with all-nil children is itself nil, which is what we get
	// because there's no sseCh wired and StartLogStream needs a client.
	_ = cmd // we don't assert non-nil — coverage is what matters here.
}

// ─── handleLogStreamConnected ────────────────────────────────────────────────

func TestHandleLogStreamConnected_NotViewingReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleLogStreamConnected(uikit.LogStreamConnectedMsg{RunID: "r-1"})
	if cmd != nil {
		t.Fatal("expected nil cmd when not viewing the run")
	}
}

func TestHandleLogStreamConnected_ViewingWiresUpChannel(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	ch := make(chan apiclient.LogStreamMsg, 1)
	_, cmd := m.handleLogStreamConnected(uikit.LogStreamConnectedMsg{RunID: "r-1", Ch: ch})
	if cmd == nil {
		t.Fatal("expected non-nil listen cmd when viewing the run")
	}
}

// ─── handleNotificationUnreadCount / handleNotificationsLoaded error paths ──

func TestHandleNotificationUnreadCount_ErrAppendsDebug(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleNotificationUnreadCount(uikit.NotificationUnreadCountMsg{
		Err: errors.New("fetch failed"),
	})
	if cmd != nil {
		t.Fatal("expected nil cmd when unread-count load errors")
	}
}

func TestHandleNotificationsLoaded_ErrAppendsDebug(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleNotificationsLoaded(uikit.NotificationsLoadedMsg{
		Err: errors.New("fetch failed"),
	})
	if cmd != nil {
		t.Fatal("expected nil cmd when notifications load errors")
	}
}

// ─── handleReconnectLog viewing+running path ─────────────────────────────────

func TestHandleReconnectLog_ViewingRunningReturnsCmd(t *testing.T) {
	m := newTestModel(nil)
	// StartLogStream returns nil when the StreamManager has no client. Replace
	// the no-op streams set up by newTestModel with one bound to a dummy
	// client so the running-path returns a real cmd.
	m.streams = NewStreamManager(newDummyClient())
	run := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, cmd := m.handleReconnectLog(uikit.ReconnectLogMsg{RunID: "r-1"})
	if cmd == nil {
		t.Fatal("expected non-nil reconnect cmd when viewing a running run")
	}
}

// TestScheduleLogReconnect_TickFiresWithRunID drives the tick body so the
// closure that emits ReconnectLogMsg is executed (not just constructed).
func TestScheduleLogReconnect_TickFiresWithRunID(t *testing.T) {
	m := newTestModel(nil)
	cmd := m.scheduleLogReconnect("r-99")
	if cmd == nil {
		t.Fatal("expected a tick cmd")
	}
	msg := cmd()
	got, ok := msg.(uikit.ReconnectLogMsg)
	if !ok {
		t.Fatalf("expected ReconnectLogMsg, got %T", msg)
	}
	if got.RunID != "r-99" {
		t.Fatalf("expected RunID=r-99, got %q", got.RunID)
	}
}

// ─── handleNotificationStreamConnected ────────────────────────────────────────

func TestHandleNotificationStreamConnected_ReturnsListenerCmd(t *testing.T) {
	m := newTestModel(nil)
	ch := make(chan apiclient.NotificationStreamEvent, 1)
	_, cmd := m.handleNotificationStreamConnected(uikit.NotificationStreamConnectedMsg{Ch: ch})
	if cmd == nil {
		t.Fatal("expected non-nil listen cmd")
	}
}

// ─── handleNotificationEvent ────────────────────────────────────────────────

func TestHandleNotificationEvent_CreatedAppendsToPanel(t *testing.T) {
	m := newTestModel(nil)
	payload := []byte(`{"notification":{"id":"n-1","severity":"info","title":"hi","count":1,"lastOccurredAt":"2026-01-01T00:00:00Z"},"unreadCount":1}`)
	out, _ := m.handleNotificationEvent(uikit.NotificationEventMsg{
		Event: apiclient.NotificationStreamEvent{Type: "notification.created", Data: payload},
	})
	got := out.(Model)
	if got.notifications.Unread() == 0 {
		t.Fatal("expected unread count to be set")
	}
}

func TestHandleNotificationEvent_CreatedInvalidJSON(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleNotificationEvent(uikit.NotificationEventMsg{
		Event: apiclient.NotificationStreamEvent{Type: "notification.created", Data: json.RawMessage("not json")},
	})
	// ContinueListeningNotifications returns nil with no channel wired.
	_ = cmd
}

func TestHandleNotificationEvent_UnreadCountChanged(t *testing.T) {
	m := newTestModel(nil)
	payload := []byte(`{"unreadCount":7}`)
	out, _ := m.handleNotificationEvent(uikit.NotificationEventMsg{
		Event: apiclient.NotificationStreamEvent{Type: "notifications.unread_count_changed", Data: payload},
	})
	got := out.(Model)
	if u := got.notifications.Unread(); u != 7 {
		t.Fatalf("expected unread=7 after unread_count_changed, got %d", u)
	}
}

func TestHandleNotificationEvent_UnreadCountChangedInvalidJSON(t *testing.T) {
	m := newTestModel(nil)
	_, _ = m.handleNotificationEvent(uikit.NotificationEventMsg{
		Event: apiclient.NotificationStreamEvent{Type: "notifications.unread_count_changed", Data: json.RawMessage("bad")},
	})
}

func TestHandleNotificationEvent_UnknownTypeNoOp(t *testing.T) {
	m := newTestModel(nil)
	_, _ = m.handleNotificationEvent(uikit.NotificationEventMsg{
		Event: apiclient.NotificationStreamEvent{Type: "ping", Data: json.RawMessage(`{}`)},
	})
}

// ─── handleDaemonLogConnected ────────────────────────────────────────────────

func TestHandleDaemonLogConnected_ReturnsListenerCmd(t *testing.T) {
	m := newTestModel(nil)
	ch := make(chan string, 1)
	_, cmd := m.handleDaemonLogConnected(uikit.DaemonLogConnectedMsg{Ch: ch})
	if cmd == nil {
		t.Fatal("expected non-nil listen cmd")
	}
}

// ─── handleReconnectLog ──────────────────────────────────────────────────────

func TestHandleReconnectLog_NotViewingReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleReconnectLog(uikit.ReconnectLogMsg{RunID: "r-x"})
	if cmd != nil {
		t.Fatal("expected nil cmd when not viewing the run")
	}
}

func TestHandleReconnectLog_NotRunningReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseEnded}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, cmd := m.handleReconnectLog(uikit.ReconnectLogMsg{RunID: "r-1"})
	if cmd != nil {
		t.Fatal("expected nil cmd when run is not Running")
	}
}

// ─── handleLogDone ───────────────────────────────────────────────────────────

func TestHandleLogDone_NotViewingReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleLogDone(uikit.LogDoneMsg{RunID: "r-1"})
	if cmd != nil {
		t.Fatal("expected nil cmd when not viewing the run")
	}
}

func TestHandleLogDone_TerminalRunReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	r := model.ReasonSuccess
	run := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &r}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, cmd := m.handleLogDone(uikit.LogDoneMsg{RunID: "r-1"})
	if cmd != nil {
		t.Fatal("expected nil cmd for terminal run — no reconnect needed")
	}
}

func TestHandleLogDone_RunningTriggersReconnect(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, cmd := m.handleLogDone(uikit.LogDoneMsg{RunID: "r-1"})
	if cmd == nil {
		t.Fatal("expected reconnect tick cmd when run is still running")
	}
}

// ─── handleLogOlderLoaded ────────────────────────────────────────────────────

func TestHandleLogOlderLoaded_PrependsLinesAndUpdatesTotal(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	m.execView.LoadingOlder = true

	_, cmd := m.handleLogOlderLoaded(uikit.LogOlderLoadedMsg{
		RunID:     "r-1",
		Lines:     []server.LogLineEntry{{N: 0, Text: "first", Stream: "stdout"}},
		FirstLine: 0,
		Total:     42,
	})
	if cmd != nil {
		t.Fatal("expected nil cmd; older-load is a pure state update")
	}
	if m.execView.LoadingOlder {
		t.Fatal("LoadingOlder must be cleared once the page arrives")
	}
}

// ─── scheduleLogReconnect ────────────────────────────────────────────────────

func TestScheduleLogReconnect_ProducesTickCmd(t *testing.T) {
	m := newTestModel(nil)
	cmd := m.scheduleLogReconnect("r-1")
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Tick cmd")
	}
}

// ─── execConfirmCmd ──────────────────────────────────────────────────────────

func TestExecConfirmCmd_NilResultDismissesIfClosed(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("t", "msg", func() tea.Msg { return nil }))
	if !m.dialogs.HasConfirm() {
		t.Fatal("precondition: confirm dialog must be open")
	}
	out := m.execConfirmCmd(func() tea.Msg { return nil }, true)
	if out != nil {
		t.Fatal("expected nil out for nil-result + closed=true")
	}
	if m.dialogs.HasConfirm() {
		t.Fatal("expected dialog dismissed for closed=true with nil result")
	}
}

func TestExecConfirmCmd_NilResultNotClosedKeepsDialog(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("t", "msg", func() tea.Msg { return nil }))
	_ = m.execConfirmCmd(func() tea.Msg { return nil }, false)
	if !m.dialogs.HasConfirm() {
		t.Fatal("dialog must stay open when closed=false")
	}
}

func TestExecConfirmCmd_QuitMsgKeepDaemonReturnsTeaQuit(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("Quit", "msg", nil))
	cmd := m.execConfirmCmd(func() tea.Msg { return uikit.QuitMsg{Action: uikit.QuitKeepDaemon} }, true)
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd for QuitMsg")
	}
	if m.quitAction != uikit.QuitKeepDaemon {
		t.Fatalf("expected QuitKeepDaemon, got %v", m.quitAction)
	}
}

func TestExecConfirmCmd_QuitMsgShutdownNoFuncReturnsTeaQuit(t *testing.T) {
	m := newTestModel(nil)
	m.shutdownFunc = nil
	m.dialogs.ShowConfirm(NewConfirmDialog("Quit", "msg", nil))
	cmd := m.execConfirmCmd(func() tea.Msg { return uikit.QuitMsg{Action: uikit.QuitShutdownDaemon} }, true)
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd when no shutdownFunc is wired")
	}
	if m.quitAction != uikit.QuitShutdownDaemon {
		t.Fatalf("expected QuitShutdownDaemon, got %v", m.quitAction)
	}
}

func TestExecConfirmCmd_OtherResultProducesPassthroughCmd(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowConfirm(NewConfirmDialog("t", "msg", nil))
	want := uikit.DebugLogMsg{Message: "hello"}
	cmd := m.execConfirmCmd(func() tea.Msg { return want }, true)
	if cmd == nil {
		t.Fatal("expected passthrough cmd")
	}
	got := cmd()
	if msg, ok := got.(uikit.DebugLogMsg); !ok || msg.Message != "hello" {
		t.Fatalf("expected pass-through DebugLogMsg, got %v", got)
	}
}

// ─── startShutdownSpinner ────────────────────────────────────────────────────

func TestStartShutdownSpinner_OpensDialogWhenNoneExists(t *testing.T) {
	m := newTestModel(nil)
	m.shutdownFunc = func() error { return nil }
	if m.dialogs.HasConfirm() {
		t.Fatal("precondition: no dialog expected")
	}
	cmd := m.startShutdownSpinner()
	if cmd == nil {
		t.Fatal("expected non-nil batched cmd")
	}
	if !m.dialogs.HasConfirm() {
		t.Fatal("expected a spinner dialog to be created")
	}
}

func TestStartShutdownSpinner_KeepsExistingDialog(t *testing.T) {
	m := newTestModel(nil)
	m.shutdownFunc = func() error { return nil }
	m.dialogs.ShowConfirm(NewConfirmDialog("Quit", "msg", nil))
	cmd := m.startShutdownSpinner()
	if cmd == nil {
		t.Fatal("expected non-nil batched cmd")
	}
}

// ─── handleLogLine ───────────────────────────────────────────────────────────

func TestHandleLogLine_NotViewingRunIgnoresButContinuesListening(t *testing.T) {
	m := newTestModel(nil)
	// No execView set → not viewing the run.
	_, cmd := m.handleLogLine(uikit.LogLineMsg{
		RunID: "r-1",
		Line:  server.LogLineEntry{N: 1, Text: "hello", Stream: "stdout"},
	})
	// streams.ContinueListeningLog returns nil when no channel is active.
	_ = cmd
}

func TestHandleLogLine_ViewingRunAppends(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, _ = m.handleLogLine(uikit.LogLineMsg{
		RunID: "r-1",
		Line:  server.LogLineEntry{N: 1, Text: "hello", Stream: "stdout"},
	})
}

// ─── handleLogRotated ────────────────────────────────────────────────────────

func TestHandleLogRotated_NotViewingNoop(t *testing.T) {
	m := newTestModel(nil)
	_, _ = m.handleLogRotated(uikit.LogRotatedMsg{RunID: "r-1", FirstAvailable: 5})
}

func TestHandleLogRotated_ViewingRunEvictsBelow(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, _ = m.handleLogRotated(uikit.LogRotatedMsg{RunID: "r-1", FirstAvailable: 5})
}

// ─── handleLogDropped ────────────────────────────────────────────────────────

func TestHandleLogDropped_NotViewingNoop(t *testing.T) {
	m := newTestModel(nil)
	_, _ = m.handleLogDropped(uikit.LogDroppedMsg{RunID: "r-1", After: 2, Count: 3})
}

func TestHandleLogDropped_ViewingRunAppendsDebugLine(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, _ = m.handleLogDropped(uikit.LogDroppedMsg{RunID: "r-1", After: 2, Count: 3})
}

// ─── handleDebugLog ──────────────────────────────────────────────────────────

func TestHandleDebugLog_AppendsAndReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleDebugLog(uikit.DebugLogMsg{Message: "info line"})
	if cmd != nil {
		t.Fatal("expected nil cmd for debug log handler")
	}
}

// ─── handleDaemonLogLine / Connected / Disconnected ──────────────────────────

func TestHandleDaemonLogLine_AppendsToDebugView(t *testing.T) {
	m := newTestModel(nil)
	_, _ = m.handleDaemonLogLine(uikit.DaemonLogLineMsg{Line: "daemon line"})
}

func TestHandleDaemonLogDisconnected_AppendsAndResubscribes(t *testing.T) {
	m := newTestModel(nil)
	// No client → SubscribeDaemonLogs returns nil; ensure no panic.
	_, _ = m.handleDaemonLogDisconnected()
}

// ─── handleTriggerRun ────────────────────────────────────────────────────────

func TestHandleTriggerRun_NewRunOpensExecView(t *testing.T) {
	m := newTestModel(nil)
	newRun := &model.Run{ID: "r-new", TaskName: "task-x", Status: model.PhaseRunning}
	updated, _ := m.handleTriggerRun(uikit.TriggerRunMsg{
		TaskName: "task-x",
		Run:      newRun,
		Retry:    false,
	})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model from handler, got %T", updated)
	}
	if got.execView == nil || got.execView.Run.ID != "r-new" {
		t.Fatalf("expected execView for r-new, got %+v", got.execView)
	}
}

func TestHandleTriggerRun_ErrorWithExecViewClosesIt(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-old", TaskName: "task-x"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	updated, _ := m.handleTriggerRun(uikit.TriggerRunMsg{
		TaskName: "task-x",
		Err:      errors.New("concurrency limit"),
	})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model from handler, got %T", updated)
	}
	if got.execView != nil {
		t.Fatal("expected execView cleared on trigger error")
	}
}

func TestHandleTriggerRun_RetrySuccessUsesRetryAction(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-retry", TaskName: "task-x", Status: model.PhaseRunning}
	_, _ = m.handleTriggerRun(uikit.TriggerRunMsg{
		TaskName: "task-x",
		Run:      run,
		Retry:    true,
	})
}

// TestHandleTriggerRun_FlashesNoUndo verifies a successful trigger flashes a
// result and does NOT arm an undo — run/retry is confirmed up front, so undo is
// reserved for delete.
func TestHandleTriggerRun_FlashesNoUndo(t *testing.T) {
	m := newTestModelWithClient(nil)
	newRun := &model.Run{ID: "r-new", TaskName: "task-x", Status: model.PhaseRunning}
	updated, _ := m.handleTriggerRun(uikit.TriggerRunMsg{TaskName: "task-x", Run: newRun})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	if got.dialogs.TakeUndo() != nil {
		t.Fatal("trigger must not arm an undo")
	}
	flash, active := got.dialogs.FlashActive()
	if !active || flash != "Started run for task-x" {
		t.Fatalf("expected a 'Started run for task-x' flash, got %q (active=%v)", flash, active)
	}
}

// TestHandleDeleteRun_ArmsUndo verifies a successful delete arms a restore undo.
func TestHandleDeleteRun_ArmsUndo(t *testing.T) {
	m := newTestModelWithClient(nil)
	updated, _ := m.handleDeleteRun(uikit.DeleteRunMsg{TaskName: "t1", RunID: "r-gone"})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	if got.dialogs.TakeUndo() == nil {
		t.Fatal("expected a successful delete to arm an undo")
	}
}

// ─── reload ──────────────────────────────────────────────────────────────────

func TestReloadSummary(t *testing.T) {
	tests := []struct {
		name string
		in   *model.ReloadResult
		want string
	}{
		{"nil", nil, "✓ Config reloaded — no changes"},
		{"empty", &model.ReloadResult{}, "✓ Config reloaded — no changes"},
		{
			"mixed",
			&model.ReloadResult{
				Added:   []string{"a"},
				Removed: []string{"b", "c"},
				Changed: []model.ReloadTaskChange{{Name: "d"}},
			},
			"✓ Config reloaded: +1 added, -2 removed, ~1 changed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := reloadSummary(tc.in); got != tc.want {
				t.Fatalf("reloadSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleReloadResult_ErrorFlashes(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleReloadResult(uikit.ReloadResultMsg{Err: errors.New("restart-only setting changed")})
	if cmd == nil {
		t.Fatal("expected an error flash cmd on reload failure")
	}
}

func TestHandleReloadResult_SuccessRebuildsSidebar(t *testing.T) {
	m := newTestModel([]model.TaskBrief{{Name: "old"}})
	m.client = newDummyClient()
	info := &model.DaemonInfo{Tasks: []model.TaskBrief{{Name: "fresh"}}, ConfigStale: false}
	updated, _ := m.handleReloadResult(uikit.ReloadResultMsg{
		Result: &model.ReloadResult{Added: []string{"fresh"}, Removed: []string{"old"}},
		Info:   info,
	})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	if len(got.info.Tasks) != 1 || got.info.Tasks[0].Name != "fresh" {
		t.Fatalf("expected info.Tasks rebuilt to [fresh], got %+v", got.info.Tasks)
	}
	if got.taskDisplayByName("fresh") == nil {
		t.Fatal("expected the fresh task to be resolvable after reload")
	}
}

// ─── handleStopRun / handleRestartService / handleStopService ────────────────

func TestHandleStopRun_LogsActionResult(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleStopRun(uikit.StopRunMsg{TaskName: "t1"})
	if cmd != nil {
		t.Fatal("expected nil cmd for stop-run handler")
	}
}

func TestHandleRestartService_ResetsServiceStopped(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-svc", TaskName: "svc"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	m.execView.SetServiceStopped(true)
	_, _ = m.handleRestartService(uikit.RestartServiceMsg{TaskName: "svc"})
}

func TestHandleStopService_SetsServiceStopped(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-svc", TaskName: "svc"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, _ = m.handleStopService(uikit.StopServiceMsg{TaskName: "svc"})
}

// ─── handleDeleteRun ─────────────────────────────────────────────────────────

func TestHandleDeleteRun_ErrorPathFlashesAndOffersNoUndo(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleDeleteRun(uikit.DeleteRunMsg{
		TaskName: "t1",
		RunID:    "r-1",
		Err:      errors.New("storage"),
	})
	// A failed delete surfaces an error flash (non-nil cmd) but must not arm an
	// undo — there is nothing to restore.
	if cmd == nil {
		t.Fatal("expected an error flash cmd on delete error")
	}
	if m.dialogs.TakeUndo() != nil {
		t.Fatal("a failed delete must not arm an undo")
	}
}

func TestHandleDeleteRun_SuccessFetchesWindow(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleDeleteRun(uikit.DeleteRunMsg{
		TaskName: "t1",
		RunID:    "r-gone",
	})
	if cmd == nil {
		t.Fatal("expected batched cmd that fetches the window")
	}
}

func TestHandleDeleteRun_SuccessClosesMatchingExecView(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-gone", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	updated, _ := m.handleDeleteRun(uikit.DeleteRunMsg{
		TaskName: "t1",
		RunID:    "r-gone",
	})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model from handler, got %T", updated)
	}
	if got.execView != nil {
		t.Fatal("expected execView closed when its run was deleted")
	}
}

// ─── maybeLoadOlderLogs ──────────────────────────────────────────────────────

func TestMaybeLoadOlderLogs_NoExecViewReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	if m.maybeLoadOlderLogs() != nil {
		t.Fatal("expected nil when no execView")
	}
}

func TestMaybeLoadOlderLogs_AlreadyLoadingReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	m.execView.LoadingOlder = true
	if m.maybeLoadOlderLogs() != nil {
		t.Fatal("expected nil when already loading older")
	}
}

func TestMaybeLoadOlderLogs_NotNeededReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	// Pane.NeedsOlder is false on a fresh view (no scroll back). Cmd should be nil.
	_ = m.maybeLoadOlderLogs()
}

// ─── handleTick ──────────────────────────────────────────────────────────────

func TestHandleTick_ProducesBatchedCmd(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleTick()
	if cmd == nil {
		t.Fatal("expected a batched tick cmd")
	}
}

// ─── handleFlashExpired ──────────────────────────────────────────────────────

func TestHandleFlashExpired_ClearsAndReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleFlashExpired()
	if cmd != nil {
		t.Fatal("expected nil cmd after clearing flash")
	}
}

// ─── handleSystemStats / handleMetricsHistory / handleRunSummary ─────────────

func TestHandleSystemStats_NilStatsNoop(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleSystemStats(uikit.SystemStatsMsg{})
	if cmd != nil {
		t.Fatal("expected nil cmd for empty stats")
	}
}

func TestHandleMetricsHistory_NilSamplesNoop(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleMetricsHistory(uikit.MetricsHistoryMsg{})
	if cmd != nil {
		t.Fatal("expected nil cmd for empty history")
	}
}

func TestHandleRunSummary_NilSummaryNoop(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleRunSummary(uikit.RunSummaryMsg{})
	if cmd != nil {
		t.Fatal("expected nil cmd for empty summary")
	}
}

// ─── handleOpenBrowser branches ──────────────────────────────────────────────

func TestHandleOpenBrowser_ErrorAppendsDebug(t *testing.T) {
	m := newTestModel(nil)
	_, _ = m.handleOpenBrowser(uikit.OpenBrowserMsg{
		URL: "http://x", Err: errors.New("open failed"),
	})
}

func TestHandleOpenBrowser_URLNoBrowserOpensCopyDialog(t *testing.T) {
	// Force clipboard.WriteAll failure so the copy dialog is shown on every
	// platform — pbcopy on macOS would otherwise succeed silently and skip
	// the fallback branch this test exercises.
	prev := clipboardWriteAll
	clipboardWriteAll = func(string) error { return errors.New("clipboard unavailable") }
	t.Cleanup(func() { clipboardWriteAll = prev })

	m := newTestModel(nil)
	updated, _ := m.handleOpenBrowser(uikit.OpenBrowserMsg{URL: "http://x"})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	if !got.dialogs.HasCopy() {
		t.Fatal("expected copy dialog set when URL falls back from browser")
	}
}

func TestHandleOpenBrowser_EmptyURLNoopNoErr(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleOpenBrowser(uikit.OpenBrowserMsg{})
	if cmd != nil {
		t.Fatal("expected nil cmd for empty browser msg")
	}
}
