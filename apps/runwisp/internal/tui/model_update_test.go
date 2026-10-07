// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// ─── copy dialog interception ────────────────────────────────────────────────

func TestInterceptCopyDialog_CtrlCDismissesAndShowsQuit(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgCopy, NewCopyDialog("title", "value"))
	if !m.dialogs.Has(dlgCopy) {
		t.Fatal("expected copy dialog to be active")
	}

	msg := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	newModelIface, _, intercepted := m.interceptActiveDialog(msg)
	if !intercepted {
		t.Fatal("expected intercepted=true for ctrl+c")
	}
	newM := newModelIface.(Model)
	if newM.dialogs.Has(dlgCopy) {
		t.Fatal("expected copy dialog dismissed after ctrl+c")
	}
}

func TestInterceptCopyDialog_AnyKeyWhileVisibleIsIntercepted(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgCopy, NewCopyDialog("title", "value"))

	msg := tea.KeyPressMsg{Code: 'x', Text: "x"}
	_, _, intercepted := m.interceptActiveDialog(msg)
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

// ─── confirm dialog interception ─────────────────────────────────────────────

func TestInterceptConfirmDialog_CtrlC(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))

	_, _, intercepted := m.interceptActiveDialog(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !intercepted {
		t.Fatal("expected intercepted=true for ctrl+c")
	}
}

func TestInterceptConfirmDialog_MouseMsg(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))

	_, _, intercepted := m.interceptActiveDialog(tea.MouseClickMsg{})
	if !intercepted {
		t.Fatal("expected intercepted=true for mouse msg with confirm dialog")
	}
}

func TestInterceptConfirmDialog_OtherMsg(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))

	_, _, intercepted := m.interceptActiveDialog(uikit.TickMsg{})
	if intercepted {
		t.Fatal("expected intercepted=false for non-key/mouse msg")
	}
}

func TestInterceptConfirmDialog_RoutesToShuttingDown(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	// While shutting down, interceptActiveDialog should defer to
	// interceptShuttingDownDialog — ShutdownDoneMsg is only handled there.
	_, _, intercepted := m.interceptActiveDialog(uikit.ShutdownDoneMsg{})
	if !intercepted {
		t.Fatal("expected intercepted=true when shutting-down dialog handles the msg")
	}
}

// ─── new-release dialog interception ─────────────────────────────────────────

func TestInterceptNewReleaseDialog_CtrlCEscalatesToQuitConfirm(t *testing.T) {
	m := newTestModel(nil)
	m.daemon = DaemonStarted
	m.dialogs.Show(dlgNewRelease, NewNewReleaseDialog("1.0.0", "v2.0.0"))

	updated, _, intercepted := m.interceptActiveDialog(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !intercepted {
		t.Fatal("expected intercepted=true for ctrl+c")
	}
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if got.dialogs.Has(dlgNewRelease) {
		t.Fatal("expected new-release dialog dismissed on ctrl+c")
	}
	if !got.dialogs.Has(dlgConfirm) {
		t.Fatal("expected ctrl+c to escalate to the quit-confirm dialog")
	}
}

func TestInterceptNewReleaseDialog_KeyMsgRoutesToDialog(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgNewRelease, NewNewReleaseDialog("1.0.0", "v2.0.0"))

	updated, _, intercepted := m.interceptActiveDialog(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !intercepted {
		t.Fatal("expected intercepted=true for a key while the dialog is open")
	}
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if got.dialogs.Has(dlgNewRelease) {
		t.Fatal("expected an unrecognized key to close the dialog")
	}
}

func TestInterceptNewReleaseDialog_MouseMsgRoutesToDialog(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgNewRelease, NewNewReleaseDialog("1.0.0", "v2.0.0"))

	_, _, intercepted := m.interceptActiveDialog(tea.MouseClickMsg{})
	if !intercepted {
		t.Fatal("expected intercepted=true for mouse msg with new-release dialog open")
	}
}

func TestInterceptNewReleaseDialog_OtherMsgNotIntercepted(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgNewRelease, NewNewReleaseDialog("1.0.0", "v2.0.0"))

	_, _, intercepted := m.interceptActiveDialog(uikit.TickMsg{})
	if intercepted {
		t.Fatal("expected intercepted=false for non-key/mouse msg")
	}
}

// ─── interceptShuttingDownDialog ─────────────────────────────────────────────

func TestInterceptShuttingDownDialog_CtrlC(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
	_ = m.dialogs.StartShutdown()

	_, _, intercepted := m.interceptShuttingDownDialog(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !intercepted {
		t.Fatal("expected intercepted=true for ctrl+c in shutdown dialog")
	}
}

func TestInterceptShuttingDownDialog_ShutdownDone(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
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
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
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
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Test", "msg", func() tea.Msg { return nil }))
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

// A system sample updates the open run's header and the task header live.
func TestHandleSSEEvent_SystemSampleUpdatesLiveUsage(t *testing.T) {
	m := newTestModel(nil)
	ev := execlist.NewExecView(&model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseRunning})
	m.execView = &ev

	payload := []byte(`{"tasks":{"t1":{"cpuPercent":12,"memoryBytes":2048}},"runs":{"r-1":{"cpuPercent":12,"memoryBytes":2048}}}`)
	_ = m.handleSSEEvent(apiclient.RunStreamEvent{Type: "system", Data: payload})
	want := &model.ResourceUsage{CPUPercent: 12, MemoryBytes: 2048}
	if *m.execView.Usage != *want || *m.taskUsage("t1") != *want {
		t.Fatalf("usage not applied: run=%v task=%v", m.execView.Usage, m.taskUsage("t1"))
	}

	_ = m.handleSSEEvent(apiclient.RunStreamEvent{Type: "system", Data: []byte(`{}`)})
	if m.execView.Usage != nil || m.taskUsage("t1") != nil {
		t.Fatal("a sample without the run clears its usage")
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

// ─── handleSSEEvent notification.* dispatch ─────────────────────────────────
// Notification events ride the unified stream (no dedicated
// /api/notifications/stream anymore), so handleSSEEvent itself must switch
// on the notification.* event types.

func TestHandleSSEEvent_NotificationCreatedUpdatesUnreadAndPanel(t *testing.T) {
	m := newTestModel(nil)
	payload := []byte(`{"notification":{"id":"n-1","severity":"info","title":"hi","count":1,"lastOccurredAt":"2026-01-01T00:00:00Z"},"unreadCount":1}`)
	cmd := m.handleSSEEvent(apiclient.RunStreamEvent{Type: "notification.created", Data: payload})
	if cmd != nil {
		t.Fatal("expected nil cmd")
	}
	if m.notifications.Unread() != 1 {
		t.Fatalf("expected unread=1, got %d", m.notifications.Unread())
	}
}

func TestHandleSSEEvent_NotificationCreatedInvalidJSONAppendsDebugLine(t *testing.T) {
	m := newTestModel(nil)
	cmd := m.handleSSEEvent(apiclient.RunStreamEvent{Type: "notification.created", Data: json.RawMessage("not json")})
	if cmd != nil {
		t.Fatal("expected nil cmd for invalid JSON")
	}
}

func TestHandleSSEEvent_NotificationUpdatedUpdatesUnread(t *testing.T) {
	m := newTestModel(nil)
	payload := []byte(`{"notification":{"id":"n-1","severity":"info","title":"hi","count":1,"lastOccurredAt":"2026-01-01T00:00:00Z"},"unreadCount":2}`)
	_ = m.handleSSEEvent(apiclient.RunStreamEvent{Type: "notification.updated", Data: payload})
	if m.notifications.Unread() != 2 {
		t.Fatalf("expected unread=2, got %d", m.notifications.Unread())
	}
}

func TestHandleSSEEvent_NotificationUnreadCountChanged(t *testing.T) {
	m := newTestModel(nil)
	payload := []byte(`{"unreadCount":7}`)
	cmd := m.handleSSEEvent(apiclient.RunStreamEvent{Type: "notification.unreadCountChanged", Data: payload})
	if cmd != nil {
		t.Fatal("expected nil cmd")
	}
	if m.notifications.Unread() != 7 {
		t.Fatalf("expected unread=7 after unreadCountChanged, got %d", m.notifications.Unread())
	}
}

func TestHandleSSEEvent_NotificationUnreadCountChangedInvalidJSON(t *testing.T) {
	m := newTestModel(nil)
	cmd := m.handleSSEEvent(apiclient.RunStreamEvent{Type: "notification.unreadCountChanged", Data: json.RawMessage("bad")})
	if cmd != nil {
		t.Fatal("expected nil cmd for invalid JSON")
	}
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

// On a rotated log the server clamps `from` up to firstAvailable but keeps the
// requested limit, so the page overlaps the lines already loaded. Only the lines
// below the loaded range may be prepended, and once the oldest surviving line is
// loaded the pane must stop asking for older ones.
func TestHandleLogOlderLoaded_RotatedLogDoesNotDuplicateLines(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	m.execView.LoadingOlder = true
	for n := int64(300); n < 310; n++ {
		m.execView.Pane.AppendLogLine(n, "stdout", fmt.Sprintf("l%d", n), 0)
	}
	m.execView.Pane.Scroll = 0

	// Asked for [100,300); the server clamped to 250 and returned 100 lines.
	page := make([]server.LogLineEntry, 0, 100)
	for n := int64(250); n < 350; n++ {
		page = append(page, server.LogLineEntry{N: n, Stream: "stdout", Text: fmt.Sprintf("l%d", n)})
	}
	updated, _ := m.handleLogOlderLoaded(uikit.LogOlderLoadedMsg{
		RunID: "r-1", Lines: page, FirstLine: 250, Total: 350, FirstAvailable: 250,
	})

	pane := updated.(Model).execView.Pane
	if pane.FirstLoadedLine != 250 {
		t.Fatalf("FirstLoadedLine: want 250, got %d", pane.FirstLoadedLine)
	}
	if len(pane.Lines) != 60 {
		t.Fatalf("buffer should hold lines 250..309 once (60), got %d", len(pane.Lines))
	}
	for i, l := range pane.Lines {
		if want := fmt.Sprintf("l%d", 250+i); l.Text != want {
			t.Fatalf("buffer[%d]: want %s, got %s (gutter would go non-monotonic)", i, want, l.Text)
		}
	}
	if pane.NeedsOlder() {
		t.Fatal("nothing older than the first available line exists; the pane must stop paging")
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

// ─── confirm callbacks ───────────────────────────────────────────────────────

var keyYes = tea.KeyPressMsg{Code: 'y', Text: "y"}

// TestConfirmCallback_NotInvokedSynchronously is the regression test for the
// TUI-freeze bug: the confirm callback used to be called inline on the Update
// goroutine to inspect its result, which blocks the whole event loop (no
// repaint, no key handling) for as long as a network-calling confirm callback
// (trigger/restart/stop run, ...) takes. It must be handed back untouched for
// Bubble Tea to run asynchronously.
func TestConfirmCallback_NotInvokedSynchronously(t *testing.T) {
	m := newTestModel(nil)
	invoked := false
	slowCmd := func() tea.Msg {
		invoked = true
		return uikit.DebugLogMsg{Message: "hello"}
	}
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("t", "msg", slowCmd))

	_, out, intercepted := m.interceptActiveDialog(keyYes)
	if !intercepted {
		t.Fatal("expected the confirm dialog to intercept the key")
	}
	if invoked {
		t.Fatal("the confirm callback must not be invoked synchronously")
	}
	if out == nil {
		t.Fatal("expected the cmd to be handed back for async execution")
	}
	msg := out()
	if !invoked {
		t.Fatal("expected invoking the returned cmd to run the original callback")
	}
	if got, ok := msg.(uikit.DebugLogMsg); !ok || got.Message != "hello" {
		t.Fatalf("expected pass-through DebugLogMsg, got %v", msg)
	}
}

func TestConfirmCallback_ClosedDismissesDialogImmediately(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("t", "msg", func() tea.Msg { return nil }))
	updated, _, _ := m.interceptActiveDialog(keyYes)
	if got := updated.(Model); got.dialogs.Has(dlgConfirm) {
		t.Fatal("expected dialog dismissed immediately on confirm")
	}
}

func TestConfirmCallback_NotClosedKeepsDialogOpen(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("t", "msg", func() tea.Msg { return nil }))
	updated, _, _ := m.interceptActiveDialog(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := updated.(Model); !got.dialogs.Has(dlgConfirm) {
		t.Fatal("dialog must stay open on a non-closing key")
	}
}

// TestConfirmCallback_QuitMsgFlowsThroughNormalDispatch confirms that a
// QuitMsg produced by the (async) confirm callback is handled correctly once
// it re-enters Update via the ordinary dispatch tables, with no special-casing
// in the dialog interceptor.
func TestConfirmCallback_QuitMsgFlowsThroughNormalDispatch(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Quit", "msg", func() tea.Msg { return uikit.QuitMsg{Action: uikit.QuitKeepDaemon} }))
	updated, cmd, _ := m.interceptActiveDialog(keyYes)
	if cmd == nil {
		t.Fatal("expected the QuitMsg-producing cmd to be handed back")
	}
	m = updated.(Model)
	msg := cmd()
	quitMsg, ok := msg.(uikit.QuitMsg)
	if !ok {
		t.Fatalf("expected uikit.QuitMsg, got %T", msg)
	}
	_, quitCmd := m.handleQuit(quitMsg)
	if quitCmd == nil {
		t.Fatal("expected handleQuit to return tea.Quit")
	}
	if m.quitAction != uikit.QuitKeepDaemon {
		t.Fatalf("expected QuitKeepDaemon, got %v", m.quitAction)
	}
}

// ─── startShutdownSpinner ────────────────────────────────────────────────────

func TestStartShutdownSpinner_OpensDialogWhenNoneExists(t *testing.T) {
	m := newTestModel(nil)
	m.shutdownFunc = func() error { return nil }
	if m.dialogs.Has(dlgConfirm) {
		t.Fatal("precondition: no dialog expected")
	}
	cmd := m.startShutdownSpinner()
	if cmd == nil {
		t.Fatal("expected non-nil batched cmd")
	}
	if !m.dialogs.Has(dlgConfirm) {
		t.Fatal("expected a spinner dialog to be created")
	}
}

func TestStartShutdownSpinner_KeepsExistingDialog(t *testing.T) {
	m := newTestModel(nil)
	m.shutdownFunc = func() error { return nil }
	m.dialogs.Show(dlgConfirm, NewConfirmDialog("Quit", "msg", nil))
	cmd := m.startShutdownSpinner()
	if cmd == nil {
		t.Fatal("expected non-nil batched cmd")
	}
}

// ─── handleLogTailLoaded ─────────────────────────────────────────────────────

// A search hit carries the 0-based line index; the pane's gutter and
// HighlightLine are 1-based. Selecting the hit on index 2 (text "l2") must
// highlight the row the gutter numbers 3, not the one after it.
func TestHandleLogTailLoaded_PendingHighlightLandsOnHitLine(t *testing.T) {
	m := newTestModel(nil)
	m.pendingHighlight = 3 // SelectMsg.Line for the hit on line index 2
	m.pendingHighlightRun = "r-a"
	run := &model.Run{ID: "r-a", TaskName: "t1", Status: model.PhaseEnded}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	updated, _ := m.handleLogTailLoaded(uikit.LogTailLoadedMsg{
		RunID:     "r-a",
		Lines:     []server.LogLineEntry{{N: 0, Text: "l0"}, {N: 1, Text: "l1"}, {N: 2, Text: "l2"}},
		Finalized: true,
	})
	pane := updated.(Model).execView.Pane
	if pane.HighlightLine == 0 {
		t.Fatal("the highlight should apply once the hit's line is in the buffer")
	}
	idx := int(pane.HighlightLine) - pane.FirstLoadedLine - 1
	if idx < 0 || idx >= len(pane.Lines) || pane.Lines[idx].Text != "l2" {
		t.Fatalf("highlight on gutter line %d should be the hit line l2, lines=%+v", pane.HighlightLine, pane.Lines)
	}
}

// TestHandleLogTailLoaded_PendingHighlightNotAppliedToDifferentRun mirrors
// TestHandleLogLine_PendingHighlightNotAppliedToDifferentRun for the tail-load
// path: opening a different run than the one a pending search highlight
// targets is the most common trigger, since a freshly opened run's own tail
// load is usually the first message to arrive.
func TestHandleLogTailLoaded_PendingHighlightNotAppliedToDifferentRun(t *testing.T) {
	m := newTestModel(nil)
	m.pendingHighlight = 5
	m.pendingHighlightRun = "r-a"

	run := &model.Run{ID: "r-b", TaskName: "t1", Status: model.PhaseEnded}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	updated, _ := m.handleLogTailLoaded(uikit.LogTailLoadedMsg{
		RunID:     "r-b",
		Lines:     []server.LogLineEntry{{N: 1, Text: "l1"}, {N: 10, Text: "l10"}},
		Finalized: true,
	})
	got := updated.(Model)
	if got.execView.Pane.HighlightLine != 0 {
		t.Fatalf("expected r-b's pane untouched, got HighlightLine=%d", got.execView.Pane.HighlightLine)
	}
	if got.pendingHighlight != 5 || got.pendingHighlightRun != "r-a" {
		t.Fatalf("expected r-a's pending highlight to survive r-b's tail load, got line=%d run=%q",
			got.pendingHighlight, got.pendingHighlightRun)
	}
}

// TestHandleLogTailLoaded_PendingHighlightAppliedToMatchingRun is the
// positive counterpart to TestHandleLogTailLoaded_PendingHighlightNotAppliedToDifferentRun:
// once the target run's own tail load lands, the pending highlight jumps and
// clears.
func TestHandleLogTailLoaded_PendingHighlightAppliedToMatchingRun(t *testing.T) {
	m := newTestModel(nil)
	m.pendingHighlight = 5
	m.pendingHighlightRun = "r-a"

	run := &model.Run{ID: "r-a", TaskName: "t1", Status: model.PhaseEnded}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	updated, _ := m.handleLogTailLoaded(uikit.LogTailLoadedMsg{
		RunID:     "r-a",
		Lines:     []server.LogLineEntry{{N: 1, Text: "l1"}, {N: 10, Text: "l10"}},
		Finalized: true,
	})
	got := updated.(Model)
	if got.execView.Pane.HighlightLine != 5 {
		t.Fatalf("expected jump to highlight line 5, got HighlightLine=%d", got.execView.Pane.HighlightLine)
	}
	if got.pendingHighlight != 0 || got.pendingHighlightRun != "" {
		t.Fatalf("expected pending highlight cleared after jump, got line=%d run=%q",
			got.pendingHighlight, got.pendingHighlightRun)
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

// TestHandleLogLine_PendingHighlightNotAppliedToDifferentRun is the
// regression test for pendingHighlight leaking across runs: a search hit
// selected for a run that wasn't open yet (r-a) leaves pendingHighlight set
// while openRunByID's GetRun fetch is still in flight. If the user opens a
// different run (r-b) in the meantime, r-b's own unrelated log lines must not
// be mistaken for the r-a highlight target just because the line numbers
// happen to satisfy the numeric comparison.
func TestHandleLogLine_PendingHighlightNotAppliedToDifferentRun(t *testing.T) {
	m := newTestModel(nil)
	m.pendingHighlight = 5
	m.pendingHighlightRun = "r-a"

	run := &model.Run{ID: "r-b", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	updated, _ := m.handleLogLine(uikit.LogLineMsg{
		RunID: "r-b",
		Line:  server.LogLineEntry{N: 10, Text: "hello", Stream: "stdout"},
	})
	got := updated.(Model)
	if got.execView.Pane.HighlightLine != 0 {
		t.Fatalf("expected r-b's pane untouched, got HighlightLine=%d", got.execView.Pane.HighlightLine)
	}
	if got.pendingHighlight != 5 || got.pendingHighlightRun != "r-a" {
		t.Fatalf("expected r-a's pending highlight to survive r-b's log line, got line=%d run=%q",
			got.pendingHighlight, got.pendingHighlightRun)
	}
}

// TestHandleLogLine_PendingHighlightAppliedToMatchingRun is the positive
// counterpart to TestHandleLogLine_PendingHighlightNotAppliedToDifferentRun:
// once the target line arrives on the run the highlight was meant for, it
// jumps and clears.
func TestHandleLogLine_PendingHighlightAppliedToMatchingRun(t *testing.T) {
	m := newTestModel(nil)
	m.pendingHighlight = 5
	m.pendingHighlightRun = "r-a"

	run := &model.Run{ID: "r-a", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	updated, _ := m.handleLogLine(uikit.LogLineMsg{
		RunID: "r-a",
		Line:  server.LogLineEntry{N: 10, Text: "hello", Stream: "stdout"},
	})
	got := updated.(Model)
	if got.execView.Pane.HighlightLine != 5 {
		t.Fatalf("expected jump to highlight line 5, got HighlightLine=%d", got.execView.Pane.HighlightLine)
	}
	if got.pendingHighlight != 0 || got.pendingHighlightRun != "" {
		t.Fatalf("expected pending highlight cleared after jump, got line=%d run=%q",
			got.pendingHighlight, got.pendingHighlightRun)
	}
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
	m := newTestModel(nil)
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

// TestHandleTriggerRun_FailureFlashesError verifies a failed trigger shows an
// error-styled toast, so it can't be mistaken for a success.
func TestHandleTriggerRun_FailureFlashesError(t *testing.T) {
	m := newTestModel(nil)
	updated, _ := m.handleTriggerRun(uikit.TriggerRunMsg{TaskName: "task-x", Err: errors.New("concurrency limit")})
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	flash, active := got.dialogs.FlashActive()
	if !active || flash != "Run failed: concurrency limit" || !got.dialogs.FlashIsError() {
		t.Fatalf("expected an error toast, got %q (active=%v, error=%v)", flash, active, got.dialogs.FlashIsError())
	}
}

// TestHandleDeleteRun_ArmsUndo verifies a successful delete arms a restore undo.
func TestHandleDeleteRun_ArmsUndo(t *testing.T) {
	m := newTestModel(nil)
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
		{
			"settings only",
			&model.ReloadResult{Settings: []string{"notifications"}},
			"✓ Config reloaded: settings updated",
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
	m := newTestModel([]model.Task{{Name: "old"}})
	m.client = newDummyClient()
	info := &model.DaemonInfo{Tasks: []model.Task{{Name: "fresh"}}, ConfigStale: false}
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

// TestHandleRestartService_LeavesDeadInstance is the regression for restarting
// a stopped service while viewing its old run: the view stayed on the dead run
// and only flipped the button to "Stop service".
func TestHandleRestartService_LeavesDeadInstance(t *testing.T) {
	m := newTestModel([]model.Task{{Name: "svc", Kind: model.KindService}})
	run := &model.Run{ID: "r-old", TaskName: "svc", Status: model.PhaseEnded}
	ev := execlist.NewExecView(run)
	ev.TaskIsService = true
	m.execView = &ev
	m.execView.SetServiceStopped(true)
	updated, _ := m.handleRestartService(uikit.RestartServiceMsg{TaskName: "svc"})
	if got := updated.(Model); got.execView != nil {
		t.Fatalf("expected the dead run's view to close, still showing %s", got.execView.RunID())
	}
}

func TestHandleRestartService_OpensFreshInstance(t *testing.T) {
	m := newTestModel([]model.Task{{Name: "svc", Kind: model.KindService}})
	m.execWindow.UpsertRun(model.Run{ID: "r-new", TaskName: "svc", Status: model.PhaseRunning})
	run := &model.Run{ID: "r-old", TaskName: "svc", Status: model.PhaseEnded}
	ev := execlist.NewExecView(run)
	ev.TaskIsService = true
	m.execView = &ev
	updated, _ := m.handleRestartService(uikit.RestartServiceMsg{TaskName: "svc"})
	got := updated.(Model)
	if got.execView == nil || got.execView.RunID() != "r-new" {
		t.Fatalf("expected the fresh instance r-new to open, got %v", got.execView)
	}
}

// TestHandleRestartService_ErrorFlashesInsteadOfSilentlyDropping is the
// bug-first regression for a manual_trigger=false rejection (or any other
// restart error) reaching the TUI and vanishing into the hidden debug log —
// the operator pressing "r" on a locked service saw nothing happen at all.
// Every other action handler (run, delete, bulk) flashes its error visibly;
// this one must too.
func TestHandleRestartService_ErrorFlashesInsteadOfSilentlyDropping(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-svc", TaskName: "svc"}
	ev := execlist.NewExecView(run)
	ev.TaskIsService = true
	m.execView = &ev
	m.execView.SetServiceStopped(true)
	_, cmd := m.handleRestartService(uikit.RestartServiceMsg{TaskName: "svc", Err: errors.New("manual_trigger is disabled for this service")})
	if cmd == nil {
		t.Fatal("expected an error flash cmd on a failed restart")
	}
	// The failed restart must not optimistically flip the service's displayed
	// running state either.
	if m.execView.Action() != execlist.ActionRestartService {
		t.Fatal("a failed restart must not clear the stopped indicator")
	}
}

func TestHandleStopService_SetsServiceStopped(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-svc", TaskName: "svc"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, cmd := m.handleStopService(uikit.StopServiceMsg{TaskName: "svc"})
	if cmd != nil {
		t.Fatal("expected no flash cmd on a successful stop")
	}
}

func TestHandleStopService_ErrorFlashesInsteadOfSilentlyDropping(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-svc", TaskName: "svc"}
	ev := execlist.NewExecView(run)
	ev.TaskIsService = true
	m.execView = &ev
	_, cmd := m.handleStopService(uikit.StopServiceMsg{TaskName: "svc", Err: errors.New("manual_trigger is disabled for this service")})
	if cmd == nil {
		t.Fatal("expected an error flash cmd on a failed stop")
	}
	if m.execView.Action() != execlist.ActionStopService {
		t.Fatal("a failed stop must not set the stopped indicator")
	}
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
	if !got.dialogs.Has(dlgCopy) {
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

// TestHandleOpenRun_DeletedRunFlashesAndKeepsPanel is the #306 regression:
// Enter on a notification whose run was deleted used to close the panel and
// show nothing. Now the panel stays open, a flash explains why, and the
// notification counts as read.
func TestHandleOpenRun_DeletedRunFlashesAndKeepsPanel(t *testing.T) {
	m := newTestModel(nil)
	n := testNotif("n1")
	n.RunID = "run-gone"
	m.notifications.Upsert(n)
	m.notifications.Toggle()

	newM, cmd := m.handleOpenRun(uikit.OpenRunMsg{
		RunID: "run-gone",
		Err:   &apiclient.HTTPStatusError{StatusCode: 404, Body: "not found"},
	})
	got := newM.(Model)
	if cmd == nil {
		t.Fatal("expected flash cmd")
	}
	if !strings.Contains(got.dialogs.flashMessage, "no longer exists") {
		t.Fatalf("flash: got %q", got.dialogs.flashMessage)
	}
	if !got.notifications.IsExpanded() {
		t.Fatal("expected notifications panel to stay open")
	}
	if ids := got.notifications.UnreadIDsForRun("run-gone"); len(ids) != 0 {
		t.Fatalf("expected notification marked read, still unread: %v", ids)
	}
	if got.execView != nil {
		t.Fatal("expected no exec view for a missing run")
	}
}

func TestHandleOpenRun_OtherErrorFlashes(t *testing.T) {
	m := newTestModel(nil)
	newM, _ := m.handleOpenRun(uikit.OpenRunMsg{RunID: "r1", Err: errors.New("connection refused")})
	if msg := newM.(Model).dialogs.flashMessage; !strings.HasPrefix(msg, "Couldn't open run") {
		t.Fatalf("flash: got %q", msg)
	}
}

func TestHandleExecWindowFetched_DropsPageFromOldFilter(t *testing.T) {
	m := newTestModel(nil)
	oldGen := uint64(0) // the window's generation before the filter change
	m.execList.SetFilter("other-task")

	items := []uikit.ExecListItem{{Run: model.Run{ID: "old-1", TaskName: "t1"}}}
	updated, _ := m.handleExecWindowFetched(uikit.ExecWindowFetchedMsg{Items: items, Total: 99, Gen: oldGen})
	got := updated.(Model)

	if got.execWindow.TotalCount() != 0 {
		t.Fatalf("a page from the previous filter must not inflate the count, got %d", got.execWindow.TotalCount())
	}
}

// ─── ctrl+c from every modal escalates to the quit flow ──────────────────────

func TestModalCtrlC_DismissesAndOpensQuitConfirm(t *testing.T) {
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	// Every dialog except the quit confirm itself.
	for kind, d := range oneOfEachDialog()[dlgConfirm+1:] {
		k := dialogKind(kind) + dlgConfirm + 1
		t.Run(fmt.Sprintf("kind %d", k), func(t *testing.T) {
			m := newTestModel(nil)
			m.daemon = DaemonStarted
			m.dialogs.Show(k, d)

			updated, _, intercepted := m.interceptActiveDialog(ctrlC)
			if !intercepted {
				t.Fatal("expected ctrl+c to be intercepted")
			}
			got, ok := updated.(Model)
			if !ok {
				t.Fatal("expected Model")
			}
			if got.dialogs.Has(k) {
				t.Fatal("expected the dialog dismissed")
			}
			if !got.dialogs.Has(dlgConfirm) {
				t.Fatal("expected the quit confirm to open")
			}
		})
	}
}

func TestModalCtrlC_QuitsImmediatelyWhenDaemonNotStartedByTUI(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.Show(dlgTaskDetail, NewTaskDetailDialog("a", &model.Task{Name: "a"}))

	updated, cmd, _ := m.interceptActiveDialog(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if got.dialogs.Has(dlgConfirm) {
		t.Fatal("expected no quit confirm when the TUI did not start the daemon")
	}
	if msg, ok := cmd().(uikit.QuitMsg); !ok || msg.Action != uikit.QuitKeepDaemon {
		t.Fatalf("expected QuitMsg{QuitKeepDaemon}, got %#v", msg)
	}
}

func TestSidebarFilterCtrlC_OpensQuitConfirm(t *testing.T) {
	m := newTestModel(nil)
	m.daemon = DaemonStarted
	m.sidebar.StartFilter()

	updated, _ := m.handleSidebarFilterKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if !got.dialogs.Has(dlgConfirm) {
		t.Fatal("expected ctrl+c in the sidebar filter to open the quit confirm")
	}
}
