// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/logsearch"
)

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keyMsgSpecial(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

// ─── handleKeyN ─────────────────────────────────────────────────────────────

func TestHandleKeyN_NoExecViewHomePageTogglesNotifications(t *testing.T) {
	m := newTestModel(nil)
	// ensure on PageHome (default) and no execView
	if m.sidebar.ActivePage() != uikit.PageHome {
		t.Fatal("expected PageHome")
	}
	if m.notifications.IsExpanded() {
		t.Fatal("expected notifications collapsed initially")
	}

	newM, _, handled := handleKeyN(m, keyMsg("n"))
	if !handled {
		t.Fatal("expected handled=true")
	}
	if !newM.notifications.IsExpanded() {
		t.Fatal("expected notifications expanded after toggle")
	}
}

func TestHandleKeyN_WithExecViewReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "run1", TaskName: "task1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	_, _, handled := handleKeyN(m, keyMsg("n"))
	if handled {
		t.Fatal("expected handled=false when execView is set")
	}
}

// ─── handleKeyEsc ────────────────────────────────────────────────────────────

func TestHandleKeyEsc_NotificationsExpanded(t *testing.T) {
	m := newTestModel(nil)
	m.notifications.Toggle()
	if !m.notifications.IsExpanded() {
		t.Fatal("expected expanded")
	}

	newM, _, handled := handleKeyEsc(m, keyMsgSpecial(tea.KeyEsc))
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.notifications.IsExpanded() {
		t.Fatal("expected notifications collapsed after esc")
	}
}

func TestHandleKeyEsc_ExecViewNonFullscreen(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	newM, _, handled := handleKeyEsc(m, keyMsgSpecial(tea.KeyEsc))
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.execView != nil {
		t.Fatal("expected execView nil after esc")
	}
}

func TestHandleKeyEsc_PanelMainFocusesSidebar(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()

	newM, _, handled := handleKeyEsc(m, keyMsgSpecial(tea.KeyEsc))
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.panelFocus != uikit.PanelSidebar {
		t.Fatalf("expected PanelSidebar, got %v", newM.panelFocus)
	}
}

func TestHandleKeyEsc_NothingApplies(t *testing.T) {
	m := newTestModel(nil)
	// default: panelFocus=PanelSidebar, no execView, no expanded notifications
	_, _, handled := handleKeyEsc(m, keyMsgSpecial(tea.KeyEsc))
	if handled {
		t.Fatal("expected handled=false")
	}
}

// ─── handleKeyBackspace ───────────────────────────────────────────────────────

func TestHandleKeyBackspace_NoExecViewReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	_, _, handled := handleKeyBackspace(m, keyMsgSpecial(tea.KeyBackspace))
	if handled {
		t.Fatal("expected handled=false when no execView")
	}
}

func TestHandleKeyBackspace_WithExecView(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	newM, _, handled := handleKeyBackspace(m, keyMsgSpecial(tea.KeyBackspace))
	if !handled {
		t.Fatal("expected handled=true with execView")
	}
	if newM.execView != nil {
		t.Fatal("expected execView closed")
	}
}

// ─── handleKeyF ──────────────────────────────────────────────────────────────

func TestHandleKeyF_NoExecViewReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	_, _, handled := handleKeyF(m, keyMsg("f"))
	if handled {
		t.Fatal("expected handled=false when no execView")
	}
}

func TestHandleKeyF_WithExecViewTogglesFullscreen(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	if m.execView.Fullscreen() {
		t.Fatal("expected not fullscreen initially")
	}
	_, _, handled := handleKeyF(m, keyMsg("f"))
	if !handled {
		t.Fatal("expected handled=true with execView")
	}
}

// ─── handleKeyLeft ────────────────────────────────────────────────────────────

func TestHandleKeyLeft_NoExecViewCallsNoExecViewHandler(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()

	newM, _, handled := handleKeyLeft(m, keyMsgSpecial(tea.KeyLeft))
	if !handled {
		t.Fatal("expected handled=true from handleKeyLeftNoExecView")
	}
	if newM.panelFocus != uikit.PanelSidebar {
		t.Fatalf("expected PanelSidebar, got %v", newM.panelFocus)
	}
}

func TestHandleKeyLeft_ExecViewAtLeftEdge(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusBack
	m.execView = &ev

	newM, _, handled := handleKeyLeft(m, keyMsgSpecial(tea.KeyLeft))
	if !handled {
		t.Fatal("expected handled=true when at left edge")
	}
	if newM.panelFocus != uikit.PanelSidebar {
		t.Fatalf("expected PanelSidebar focus, got %v", newM.panelFocus)
	}
}

// ─── handleKeyLeftNoExecView ─────────────────────────────────────────────────

func TestHandleKeyLeftNoExecView_MainPanelNotDebug(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	// default page is PageHome, not PageDebug
	newM, _, handled := handleKeyLeftNoExecView(m)
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.panelFocus != uikit.PanelSidebar {
		t.Fatal("expected sidebar focus")
	}
}

// ─── isAtExecViewLeftEdge ─────────────────────────────────────────────────────

func TestIsAtExecViewLeftEdge_FocusBack(t *testing.T) {
	ev := &execlist.ExecView{HeaderFocus: execlist.HeaderFocusBack}
	if !isAtExecViewLeftEdge(ev) {
		t.Fatal("HeaderFocusBack should be at left edge")
	}
}

func TestIsAtExecViewLeftEdge_FocusStarted(t *testing.T) {
	ev := &execlist.ExecView{HeaderFocus: execlist.HeaderFocusStarted}
	if !isAtExecViewLeftEdge(ev) {
		t.Fatal("HeaderFocusStarted should be at left edge")
	}
}

func TestIsAtExecViewLeftEdge_FocusNoneHScrollZero(t *testing.T) {
	ev := &execlist.ExecView{HeaderFocus: execlist.HeaderFocusNone}
	// Pane.HScroll defaults to 0
	if !isAtExecViewLeftEdge(ev) {
		t.Fatal("HeaderFocusNone with HScroll=0 should be at left edge")
	}
}

func TestIsAtExecViewLeftEdge_FocusNoneHScrollPositive(t *testing.T) {
	ev := &execlist.ExecView{HeaderFocus: execlist.HeaderFocusNone}
	ev.Pane.HScroll = 5
	if isAtExecViewLeftEdge(ev) {
		t.Fatal("HeaderFocusNone with HScroll>0 should NOT be at left edge")
	}
}

// ─── handleKeyRight ───────────────────────────────────────────────────────────

func TestHandleKeyRight_NoExecViewMainPanelHomePageFocusesMain(t *testing.T) {
	m := newTestModel(nil)
	// default: PanelSidebar, PageHome
	newM, _, handled := handleKeyRight(m, keyMsgSpecial(tea.KeyRight))
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.panelFocus != uikit.PanelMain {
		t.Fatal("expected PanelMain")
	}
}

func TestHandleKeyRight_NoExecViewMainPanelDebugPageReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	// Navigate to Debug page: no tasks → items are [Home(0), Info(1), Debug(2)]
	selectSidebarItem(&m, 2)
	if m.sidebar.ActivePage() != uikit.PageDebug {
		t.Fatalf("expected PageDebug, got %v", m.sidebar.ActivePage())
	}
	// Now focus the main panel while Debug is active.
	m.panelFocus = uikit.PanelMain

	_, _, handled := handleKeyRight(m, keyMsgSpecial(tea.KeyRight))
	if handled {
		t.Fatal("expected handled=false when on PageDebug with PanelMain")
	}
}

func TestHandleKeyRight_ExecViewFullscreenReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.ToggleFullscreen()
	m.execView = &ev

	_, _, handled := handleKeyRight(m, keyMsgSpecial(tea.KeyRight))
	if handled {
		t.Fatal("expected handled=false when execView is fullscreen")
	}
}

func TestHandleKeyRight_ExecViewNonFullscreenSidebarFocuseMain(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	// panelFocus is PanelSidebar by default

	newM, _, handled := handleKeyRight(m, keyMsgSpecial(tea.KeyRight))
	if !handled {
		t.Fatal("expected handled=true when sidebar focused and execView non-fullscreen")
	}
	if newM.panelFocus != uikit.PanelMain {
		t.Fatalf("expected PanelMain, got %v", newM.panelFocus)
	}
}

func TestHandleKeyRight_ExecViewNonFullscreenMainPanelReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	m.panelFocus = uikit.PanelMain

	_, _, handled := handleKeyRight(m, keyMsgSpecial(tea.KeyRight))
	if handled {
		t.Fatal("expected handled=false when already on main panel with non-fullscreen execView")
	}
}

// ─── handleKeyEnter ───────────────────────────────────────────────────────────

func TestHandleKeyEnter_MainPanelNoExecViewHomeCursorGte0(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	m.homeCursor = 0

	_, _, handled := handleKeyEnter(m, keyMsgSpecial(tea.KeyEnter))
	if !handled {
		t.Fatal("expected handled=true for homeCursor>=0 path")
	}
}

func TestHandleKeyEnter_SidebarWithExecView(t *testing.T) {
	m := newTestModel(nil)
	// panelFocus = PanelSidebar by default
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	newM, _, handled := handleKeyEnter(m, keyMsgSpecial(tea.KeyEnter))
	if handled {
		t.Fatal("expected handled=false for sidebar+execView path (delegates to sidebar)")
	}
	if newM.execView != nil {
		t.Fatal("expected execView cleared by the sidebar+execView path")
	}
}

// ─── handleKeyEnterNotifications ─────────────────────────────────────────────

func TestHandleKeyEnterNotifications_NilSelected(t *testing.T) {
	m := newTestModel(nil)
	// no notifications seeded, selected == nil
	_, _, handled := handleKeyEnterNotifications(m)
	if !handled {
		t.Fatal("expected handled=true even for nil selection")
	}
}

// TestHandleKeyEnterNotifications_SelectedWithRunID covers the
// `sel != nil && sel.RunID != ""` branch by seeding a notification carrying a
// RunID and expanding the panel so Selected() is non-nil.
func TestHandleKeyEnterNotifications_SelectedWithRunID(t *testing.T) {
	m := newTestModel(nil)
	n := testNotif("n1")
	n.RunID = "run-1"
	n.TaskName = "t1"
	m.notifications.Upsert(n)
	m.notifications.Toggle()

	if m.notifications.Selected() == nil {
		t.Fatal("precondition: expected a selection after Upsert+Toggle")
	}

	_, _, handled := handleKeyEnterNotifications(m)
	if !handled {
		t.Fatal("expected handled=true for selected with RunID")
	}
}

// ─── handleKeyEnterHeader ─────────────────────────────────────────────────────

func TestHandleKeyEnterHeader_FocusBack(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusBack
	m.execView = &ev

	newM, _, handled := handleKeyEnterHeader(m)
	if !handled {
		t.Fatal("expected handled=true for HeaderFocusBack")
	}
	if newM.execView != nil {
		t.Fatal("expected closeExecView to clear execView")
	}
}

// ─── handleKeyEnterActionButton ───────────────────────────────────────────────

// TestHandleKeyEnterActionButton_NilClient_Actions covers the four ev.Action()
// branches that route through confirmAction → nil-client guard → nil cmd.
// Each row picks an execView state that produces a distinct Action() and
// verifies the handler still reports handled=true.
func TestHandleKeyEnterActionButton_NilClient_Actions(t *testing.T) {
	reason := model.ReasonFailed
	tests := []struct {
		name string
		make func() execlist.ExecView
	}{
		{
			"ActionStop (running, non-service)",
			func() execlist.ExecView {
				return execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning})
			},
		},
		{
			"ActionStopService (running service)",
			func() execlist.ExecView {
				ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning})
				ev.TaskIsService = true
				return ev
			},
		},
		{
			"ActionRetry (ended, retryable)",
			func() execlist.ExecView {
				return execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &reason})
			},
		},
		{
			"ActionRestartService (service stopped)",
			func() execlist.ExecView {
				ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1"})
				ev.TaskIsService = true
				ev.SetServiceStopped(true)
				return ev
			},
		},
		{
			"ActionDelete (ended, non-retryable, non-service)",
			func() execlist.ExecView {
				success := model.ReasonSuccess
				return execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &success})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(nil)
			ev := tt.make()
			m.execView = &ev

			_, cmd, handled := handleKeyEnterActionButton(m)
			if !handled {
				t.Fatal("expected handled=true")
			}
			if cmd != nil {
				t.Fatalf("expected nil cmd with nil client, got %v", cmd)
			}
		})
	}
}

// ─── handleKeyEnterMainPanel ──────────────────────────────────────────────────

func TestHandleKeyEnterMainPanel_HomeCursorGte0(t *testing.T) {
	m := newTestModel(nil)
	m.homeCursor = 0

	_, _, handled := handleKeyEnterMainPanel(m)
	if !handled {
		t.Fatal("expected handled=true for homeCursor>=0")
	}
}

func TestHandleKeyEnterMainPanel_RunNil(t *testing.T) {
	m := newTestModel(nil)
	m.homeCursor = -1
	// execList is empty, SelectedRun() == nil

	_, _, handled := handleKeyEnterMainPanel(m)
	if handled {
		t.Fatal("expected handled=false when no run selected")
	}
}

// ─── handleKeyS ───────────────────────────────────────────────────────────────

func TestHandleKeyS_NoExecViewReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	_, _, handled := handleKeyS(m, keyMsg("s"))
	if handled {
		t.Fatal("expected handled=false when no execView")
	}
}

// TestHandleKeyS_ExecViewActions exercises the three execView-present
// branches of handleKeyS — ActionStop, ActionStopService, and a non-stop
// action — under a nil client. Each path should report handled=true with a
// nil cmd because confirmAction short-circuits on the nil client.
func TestHandleKeyS_ExecViewActions(t *testing.T) {
	reason := model.ReasonFailed
	tests := []struct {
		name string
		make func() execlist.ExecView
	}{
		{
			"ActionStop (running, non-service)",
			func() execlist.ExecView {
				return execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning})
			},
		},
		{
			"ActionStopService (running service)",
			func() execlist.ExecView {
				ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning})
				ev.TaskIsService = true
				return ev
			},
		},
		{
			"non-stop action (ended, retryable)",
			func() execlist.ExecView {
				return execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &reason})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(nil)
			ev := tt.make()
			m.execView = &ev

			_, cmd, handled := handleKeyS(m, keyMsg("s"))
			if !handled {
				t.Fatal("expected handled=true")
			}
			if cmd != nil {
				t.Fatalf("expected nil cmd with nil client, got %v", cmd)
			}
		})
	}
}

// ─── handleKeyD ───────────────────────────────────────────────────────────────

func TestHandleKeyD_NoExecViewReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	_, _, handled := handleKeyD(m, keyMsg("d"))
	if handled {
		t.Fatal("expected handled=false when no execView")
	}
}

func TestHandleKeyDCapital_DeletableRun_ReturnsHandled(t *testing.T) {
	m := newTestModel(nil)
	success := model.ReasonSuccess
	ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &success})
	m.execView = &ev

	_, cmd, handled := handleKeyD(m, keyMsg("D"))
	if !handled {
		t.Fatal("expected handled=true for D on deletable run")
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd with nil client, got %v", cmd)
	}
}

func TestHandleKeyDCapital_RunningRun_NoOp(t *testing.T) {
	m := newTestModel(nil)
	ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning})
	m.execView = &ev

	_, cmd, handled := handleKeyD(m, keyMsg("D"))
	if !handled {
		t.Fatal("expected handled=true (swallowed) for D on running run")
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd for non-deletable run, got %v", cmd)
	}
}

// ─── handleKeyUp ──────────────────────────────────────────────────────────────

func TestHandleKeyUp_MainPanelNoExecViewHomeNoActiveTask(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	// PageHome, no active task, no execView

	// handleKeyUpHome: execList empty and homeCursor<0 and no fields → returns false
	_, _, handled := handleKeyUp(m, keyMsgSpecial(tea.KeyUp))
	if handled {
		t.Fatal("expected handled=false when no fields and no exec items")
	}
}

// ─── handleKeyUpHome ──────────────────────────────────────────────────────────

func TestHandleKeyUpHome_EmptyFieldsEmptyList(t *testing.T) {
	m := newTestModel(nil)
	// No port set -> Fields() returns empty -> handled=false
	_, _, handled := handleKeyUpHome(m)
	if handled {
		t.Fatal("expected handled=false when no fields and empty list")
	}
}

func TestHandleKeyUpHome_HomeCursorNegativeWithFields(t *testing.T) {
	m := newTestModel(nil)
	// Set up info so Fields() returns something
	m.info.Port = 8080
	m.info.WebUIDisabled = false
	m.homeCursor = -1

	newM, cmd, handled := handleKeyUpHome(m)
	if !handled {
		t.Fatal("expected handled=true when fields exist and cursor<0")
	}
	_ = cmd
	// Should have moved cursor to last field
	if newM.homeCursor < 0 {
		t.Fatal("expected homeCursor >= 0 after pressing up")
	}
}

func TestHandleKeyUpHome_HomeCursorPositiveDecrement(t *testing.T) {
	m := newTestModel(nil)
	m.info.Port = 8080
	m.info.WebUIDisabled = false
	m.homeCursor = 1

	newM, _, handled := handleKeyUpHome(m)
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.homeCursor != 0 {
		t.Fatalf("expected homeCursor=0, got %d", newM.homeCursor)
	}
}

// ─── handleKeyDown ────────────────────────────────────────────────────────────

func TestHandleKeyDown_MainPanelHomeCursorGte0(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	m.homeCursor = 0
	m.info.Port = 8080
	m.info.WebUIDisabled = false

	// handleKeyDownHome: cursor at 0, fields has items
	_, _, handled := handleKeyDown(m, keyMsgSpecial(tea.KeyDown))
	if !handled {
		t.Fatal("expected handled=true")
	}
}

// ─── handleKeyDownHome ────────────────────────────────────────────────────────

func TestHandleKeyDownHome_LastFieldResetsCursor(t *testing.T) {
	m := newTestModel(nil)
	// Only FieldWebUI (no launchTicket, no password)
	m.info.Port = 8080
	m.info.WebUIDisabled = false
	// Set cursor to last field index
	m.homeCursor = 0 // WebUI is only field (index 0)

	newM, _, handled := handleKeyDownHome(m)
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.homeCursor != -1 {
		t.Fatalf("expected homeCursor=-1 at last field, got %d", newM.homeCursor)
	}
}

// ─── handleKeyBackspace (fullscreen) ─────────────────────────────────────────

func TestHandleKeyBackspace_WithFullscreenExecView(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.ToggleFullscreen()
	m.execView = &ev

	newM, _, handled := handleKeyBackspace(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if !handled {
		t.Fatal("expected handled=true for backspace on fullscreen exec view")
	}
	if newM.execView.Fullscreen() {
		t.Fatal("expected fullscreen=false after backspace")
	}
}

// ─── handleKeyEnterHeader (FocusAction, FocusCopyField) ──────────────────────

func TestHandleKeyEnterHeader_FocusAction(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusAction
	m.execView = &ev
	_, _, handled := handleKeyEnterHeader(m)
	if !handled {
		t.Fatal("expected handled=true for HeaderFocusAction")
	}
}

func TestHandleKeyEnterHeader_FocusCopyField(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusStarted
	m.execView = &ev
	_, _, handled := handleKeyEnterHeader(m)
	if !handled {
		t.Fatal("expected handled=true for HeaderFocusStarted (copy field)")
	}
}

// ─── handleKeyR (notifications expanded, R key, execView retry/restart) ───────

func TestHandleKeyR_NotificationsExpanded(t *testing.T) {
	m := newTestModel(nil)
	m.notifications.Upsert(server.NotificationDTO{ID: "n1", Severity: "error", Count: 1, Title: "t"})
	m.notifications.Toggle()
	_, _, handled := handleKeyR(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !handled {
		t.Fatal("expected handled=true when notifications expanded")
	}
}

func TestHandleKeyR_CapitalRReloadsConfig(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	_, _, handled := handleKeyR(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if !handled {
		t.Fatal("expected capital R to be handled (triggers config reload)")
	}
}

func TestHandleKeyR_WithExecViewRetry(t *testing.T) {
	m := newTestModel(nil)
	reason := model.ReasonFailed
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &reason}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	_, _, handled := handleKeyRExecView(m)
	if !handled {
		t.Fatal("expected handled=true for execView with retry action")
	}
}

// ─── handleKeyUp/Down (notifications) ────────────────────────────────────────

func TestHandleKeyUp_NotificationsScrollable(t *testing.T) {
	m := newTestModel(nil)
	for i := 0; i < 5; i++ {
		m.notifications.Upsert(server.NotificationDTO{
			ID:       "n" + string(rune('0'+i)),
			Severity: "error",
			Count:    1,
			Title:    "x",
		})
	}
	m.notifications.Toggle()
	_, _, handled := handleKeyUp(m, tea.KeyMsg{Type: tea.KeyUp})
	if !handled {
		t.Fatal("expected handled=true when notifications expanded and can scroll")
	}
}

func TestHandleKeyDown_NotificationsScrollable(t *testing.T) {
	m := newTestModel(nil)
	for i := 0; i < 5; i++ {
		m.notifications.Upsert(server.NotificationDTO{
			ID:       "n" + string(rune('0'+i)),
			Severity: "error",
			Count:    1,
			Title:    "x",
		})
	}
	m.notifications.Toggle()
	_, _, handled := handleKeyDown(m, tea.KeyMsg{Type: tea.KeyDown})
	if !handled {
		t.Fatal("expected handled=true when notifications expanded")
	}
}

// ─── handleKeyHelp ──────────────────────────────────────────────────────────

func TestHandleKeyHelp_OpensOverlay(t *testing.T) {
	m := newTestModel(nil)

	updated, _ := m.handleKey(keyMsg("?"))
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if !got.dialogs.HasHelp() {
		t.Fatal("expected '?' to open the help overlay")
	}
}

func TestHandleKeyHelp_SecondQuestionMarkCloses(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowHelp()

	updated, _ := m.Update(keyMsg("?"))
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if got.dialogs.HasHelp() {
		t.Fatal("expected '?' to close an open help overlay")
	}
}

func TestHandleKeyHelp_CtrlCEscalatesToQuitConfirm(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowHelp()

	updated, _ := m.Update(keyMsgSpecial(tea.KeyCtrlC))
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if got.dialogs.HasHelp() {
		t.Fatal("expected ctrl+c to dismiss the help overlay")
	}
	if !got.dialogs.HasConfirm() {
		t.Fatal("expected ctrl+c to open the quit confirm")
	}
}

func TestInterceptHelpDialog_ScrollKeyConsumedAndStaysOpen(t *testing.T) {
	m := newTestModel(nil)
	m.dialogs.ShowHelp()

	got, _, intercepted := m.interceptHelpDialog(keyMsgSpecial(tea.KeyDown))
	if !intercepted {
		t.Fatal("expected arrow-down to be consumed by the modal help overlay")
	}
	gotModel := got.(Model)
	if !gotModel.dialogs.HasHelp() {
		t.Fatal("expected help overlay to remain open")
	}
}

func TestInterceptHelpDialog_CloseKeysIntercept(t *testing.T) {
	closeKeys := []tea.Msg{
		keyMsg("?"),
		keyMsgSpecial(tea.KeyEsc),
		keyMsgSpecial(tea.KeyEnter),
		keyMsg("q"),
	}
	for _, key := range closeKeys {
		m := newTestModel(nil)
		m.dialogs.ShowHelp()

		got, _, intercepted := m.interceptHelpDialog(key)
		if !intercepted {
			t.Fatalf("expected close key %v to be intercepted", key)
		}
		gotModel := got.(Model)
		if gotModel.dialogs.HasHelp() {
			t.Fatalf("expected help overlay dismissed after close key %v", key)
		}
	}
}

// ─── handleKeyQuit ───────────────────────────────────────────────────────────

func TestHandleKeyQuit_OpensQuitConfirmDialog(t *testing.T) {
	m := newTestModel(nil)
	if m.dialogs.HasConfirm() {
		t.Fatal("precondition: no dialog expected")
	}
	newM, cmd, handled := handleKeyQuit(m, keyMsg("q"))
	if !handled {
		t.Fatal("expected handled=true")
	}
	if cmd != nil {
		t.Fatal("handleKeyQuit returns nil cmd; showQuitConfirm enqueues the dialog directly")
	}
	if !newM.dialogs.HasConfirm() {
		t.Fatal("expected a confirm dialog to be queued")
	}
}

// ─── delegateToExecList ──────────────────────────────────────────────────────

func TestDelegateToExecList_EmptyListReturnsEmptyCmds(t *testing.T) {
	m := newTestModel(nil)
	cmds := delegateToExecList(&m, keyMsgSpecial(tea.KeyDown))
	// Empty exec list, nil client → no fetch cmd, possibly no update cmd.
	for _, c := range cmds {
		if c == nil {
			t.Fatal("delegateToExecList must not return nil entries in cmds slice")
		}
	}
}

// ─── handleKey (global handler dispatch + unknown key) ───────────────────────

// TestHandleKey_UnknownKeyDelegates verifies that a key not in
// globalKeyHandlers falls through to delegateKeyToFocusedView.
func TestHandleKey_UnknownKeyDelegates(t *testing.T) {
	m := newTestModel(nil)
	// "x" is not in globalKeyHandlers; expect delegation to sub-component.
	newM, _ := m.handleKey(keyMsg("x"))
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model return, got %T", newM)
	}
}

// TestHandleKey_GlobalHandledKey verifies a global key (q) takes the
// `handled=true` branch and returns early.
func TestHandleKey_GlobalHandledKey(t *testing.T) {
	m := newTestModel(nil)
	newM, _ := m.handleKey(keyMsg("q"))
	got, ok := newM.(Model)
	if !ok {
		t.Fatalf("expected Model return, got %T", newM)
	}
	if !got.dialogs.HasConfirm() {
		t.Fatal("expected quit dialog after pressing q")
	}
}

// ─── delegateKeyToFocusedView paths ───────────────────────────────────────────

// TestDelegateKeyToFocusedView_ExecViewMainPanel covers the execView-with-main-
// panel branch. With no log lines loaded, maybeLoadOlderLogs returns nil.
func TestDelegateKeyToFocusedView_ExecViewMainPanel(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.SetFocused(true)
	m.execView = &ev
	m.focusMainPanel()

	newM, _ := m.delegateKeyToFocusedView(keyMsgSpecial(tea.KeyDown))
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
}

// TestDelegateKeyToFocusedView_SidebarBranch covers the panelFocus==Sidebar
// branch which forwards through sidebar.Update + applySidebarSelectionChange.
func TestDelegateKeyToFocusedView_SidebarBranch(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup"}}
	m := newTestModel(tasks)
	// Default focus is Sidebar.
	newM, _ := m.delegateKeyToFocusedView(keyMsg("j"))
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
}

// TestDelegateKeyToFocusedView_InfoPage covers the info-page branch.
func TestDelegateKeyToFocusedView_InfoPage(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "t1"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 2) // Info
	m.focusMainPanel()
	newM, _ := m.delegateKeyToFocusedView(keyMsgSpecial(tea.KeyDown))
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
}

// TestDelegateKeyToFocusedView_DebugPage covers the debug-page branch.
func TestDelegateKeyToFocusedView_DebugPage(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "t1"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 3) // Debug
	m.focusMainPanel()
	newM, _ := m.delegateKeyToFocusedView(keyMsgSpecial(tea.KeyDown))
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
}

// TestDelegateKeyToFocusedView_MainPanelHomeDelegatesToExecList covers the
// fall-through branch that delegates to the exec list.
func TestDelegateKeyToFocusedView_MainPanelHomeDelegatesToExecList(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	m.homeCursor = -1 // ensure delegateToExecList branch
	newM, _ := m.delegateKeyToFocusedView(keyMsgSpecial(tea.KeyDown))
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
}

// ─── handleKeyEsc additional branches ─────────────────────────────────────────

// TestHandleKeyEsc_FullscreenExecViewExitsFullscreen covers the
// `execView.Fullscreen()==true` branch.
func TestHandleKeyEsc_FullscreenExecViewExitsFullscreen(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.ToggleFullscreen()
	m.execView = &ev

	newM, _, handled := handleKeyEsc(m, keyMsgSpecial(tea.KeyEsc))
	if !handled {
		t.Fatal("expected handled=true for esc on fullscreen exec view")
	}
	if newM.execView.Fullscreen() {
		t.Fatal("expected fullscreen=false after esc")
	}
}

// ─── handleKeyLeft additional branches ──────────────────────────────────────

// TestHandleKeyLeft_ExecViewFullscreenReturnsFalse covers the fullscreen
// branch where Left is delegated to scroll panning.
func TestHandleKeyLeft_ExecViewFullscreenReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.ToggleFullscreen()
	m.execView = &ev

	_, _, handled := handleKeyLeft(m, keyMsgSpecial(tea.KeyLeft))
	if handled {
		t.Fatal("expected handled=false for left on fullscreen exec view")
	}
}

// TestHandleKeyLeft_ExecViewNotAtLeftEdge covers the not-at-left-edge branch.
func TestHandleKeyLeft_ExecViewNotAtLeftEdge(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusID // not a left-edge focus
	m.execView = &ev

	_, _, handled := handleKeyLeft(m, keyMsgSpecial(tea.KeyLeft))
	if handled {
		t.Fatal("expected handled=false when not at left edge")
	}
}

// TestHandleKeyLeftNoExecView_DebugWithHScroll covers the
// debug-page-with-hscroll>0 branch which yields handled=false.
func TestHandleKeyLeftNoExecView_DebugWithHScroll(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "t1"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 3) // Debug
	m.focusMainPanel()
	m.debugView.pane.HScroll = 5

	_, _, handled := handleKeyLeftNoExecView(m)
	if handled {
		t.Fatal("expected handled=false when on Debug page with HScroll>0")
	}
}

// ─── handleKeyEnter additional branches ─────────────────────────────────────

// TestHandleKeyEnter_NotificationsExpanded covers the notifications-expanded
// branch.
func TestHandleKeyEnter_NotificationsExpanded(t *testing.T) {
	m := newTestModel(nil)
	m.notifications.Toggle()
	_, _, handled := handleKeyEnter(m, keyMsgSpecial(tea.KeyEnter))
	if !handled {
		t.Fatal("expected handled=true on notifications branch")
	}
}

// TestHandleKeyEnter_ExecViewHeaderFocus covers the execView with
// HeaderFocus != None branch.
func TestHandleKeyEnter_ExecViewHeaderFocus(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusBack
	m.execView = &ev
	m.focusMainPanel()

	_, _, handled := handleKeyEnter(m, keyMsgSpecial(tea.KeyEnter))
	if !handled {
		t.Fatal("expected handled=true for execView header focus")
	}
}

// TestHandleKeyEnter_BareNoOp covers the final fall-through (handled=false).
func TestHandleKeyEnter_BareNoOp(t *testing.T) {
	m := newTestModel(nil)
	// default: sidebar focus, no execView, no notifications expanded
	_, _, handled := handleKeyEnter(m, keyMsgSpecial(tea.KeyEnter))
	if handled {
		t.Fatal("expected handled=false in default sidebar/no-execView state")
	}
}

// ─── handleKeyEnterHeader: HeaderFocusDuration ────────────────────────────────

func TestHandleKeyEnterHeader_FocusDuration(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusDuration
	m.execView = &ev
	_, _, handled := handleKeyEnterHeader(m)
	if !handled {
		t.Fatal("expected handled=true for HeaderFocusDuration (copy field)")
	}
}

// TestHandleKeyEnterHeader_FocusNone covers the default branch — handled=true
// with nil cmd.
func TestHandleKeyEnterHeader_FocusNone(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusNone
	m.execView = &ev
	_, _, handled := handleKeyEnterHeader(m)
	if !handled {
		t.Fatal("expected handled=true for default HeaderFocus")
	}
}

// ─── handleKeyEnterActionButton: ActionDelete ────────────────────────────────

func TestHandleKeyEnterActionButton_ActionDelete(t *testing.T) {
	m := newTestModel(nil)
	success := model.ReasonSuccess
	ev := execlist.NewExecView(&model.Run{
		ID:        "r-1234567890",
		TaskName:  "t1",
		Status:    model.PhaseEnded,
		EndReason: &success,
	})
	m.execView = &ev
	_, _, handled := handleKeyEnterActionButton(m)
	if !handled {
		t.Fatal("expected handled=true for ActionDelete")
	}
}

// ─── handleKeyR: cron task path (no execView) ────────────────────────────────

func TestHandleKeyR_CronTaskTriggers(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 1)
	m.client = newDummyClient()
	_, _, handled := handleKeyR(m, keyMsg("r"))
	if !handled {
		t.Fatal("expected handled=true for r on cron task")
	}
}

// ─── handleKeyRExecView: ActionRestartService and default branches ──────────

func TestHandleKeyRExecView_RestartService(t *testing.T) {
	m := newTestModel(nil)
	ev := execlist.NewExecView(&model.Run{ID: "r-1234567890", TaskName: "t1"})
	ev.TaskIsService = true
	ev.SetServiceStopped(true)
	m.execView = &ev

	_, _, handled := handleKeyRExecView(m)
	if !handled {
		t.Fatal("expected handled=true for restart-service action")
	}
}

func TestHandleKeyRExecView_DefaultAction(t *testing.T) {
	m := newTestModel(nil)
	// Pending status produces an action like ActionStop or none; in any case
	// the default branch returns handled=true with nil cmd.
	ev := execlist.NewExecView(&model.Run{ID: "r-1234567890", TaskName: "t1", Status: model.PhasePending})
	m.execView = &ev
	_, _, handled := handleKeyRExecView(m)
	if !handled {
		t.Fatal("expected handled=true for default exec view r")
	}
}

// ─── handleKeyD: fullscreen branch ───────────────────────────────────────────

func TestHandleKeyD_FullscreenReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	ev := execlist.NewExecView(&model.Run{ID: "r-1234567890", TaskName: "t1"})
	ev.ToggleFullscreen()
	m.execView = &ev
	_, _, handled := handleKeyD(m, keyMsg("d"))
	if handled {
		t.Fatal("expected handled=false on fullscreen exec view")
	}
}

// TestHandleKeyD_LowerCaseDDownload covers the lowercase-d download branch.
func TestHandleKeyD_LowerCaseDDownload(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	m := newTestModel(nil)
	m.launchTicketFunc = func() (string, error) { return "tkt", nil }
	ev := execlist.NewExecView(&model.Run{ID: "r-1234567890", TaskName: "t1"})
	m.execView = &ev

	_, cmd, handled := handleKeyD(m, keyMsg("d"))
	if !handled {
		t.Fatal("expected handled=true for lowercase d")
	}
	if cmd == nil {
		t.Fatal("expected non-nil download cmd")
	}
}

// ─── handleKeyDown additional branches ───────────────────────────────────────

// TestHandleKeyDown_NoCursorReturnsFalse covers the no-homeCursor branch.
func TestHandleKeyDown_NoCursorReturnsFalse(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	m.homeCursor = -1
	_, _, handled := handleKeyDown(m, keyMsgSpecial(tea.KeyDown))
	if handled {
		t.Fatal("expected handled=false when homeCursor=-1")
	}
}

// ─── handleKeyDownHome (cursor < last field) ─────────────────────────────────

func TestHandleKeyDownHome_IncrementsWithinFields(t *testing.T) {
	m := newTestModel(nil)
	m.info.Port = 8181
	m.info.PasswordEphemeral = true
	m.info.Password = "swordfish"
	// Fields: [FieldWebUI(0), FieldPassword(1)] (no launch ticket)
	m.homeCursor = 0

	newM, _, handled := handleKeyDownHome(m)
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.homeCursor != 1 {
		t.Fatalf("expected homeCursor=1 after increment, got %d", newM.homeCursor)
	}
}

// ─── handleKeyUp additional branches ─────────────────────────────────────────

// TestHandleKeyUp_NotificationsBoundaryFlash covers the BumpBoundaryFlash
// branch where MoveCursor(-1) fails at the top boundary.
func TestHandleKeyUp_NotificationsBoundaryFlash(t *testing.T) {
	m := newTestModel(nil)
	m.notifications.Upsert(server.NotificationDTO{ID: "n1", Severity: "info", Count: 1, Title: "x"})
	m.notifications.Toggle()
	// Cursor at 0 already; up should hit boundary.
	_, cmd, handled := handleKeyUp(m, keyMsgSpecial(tea.KeyUp))
	if !handled {
		t.Fatal("expected handled=true")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd (ScheduleFlashClear)")
	}
}

// TestHandleKeyDown_NotificationsBoundaryFlash mirrors the same for down.
func TestHandleKeyDown_NotificationsBoundaryFlash(t *testing.T) {
	m := newTestModel(nil)
	m.notifications.Upsert(server.NotificationDTO{ID: "n1", Severity: "info", Count: 1, Title: "x"})
	m.notifications.Toggle()
	// Cursor at 0 on a single-item panel: down hits boundary immediately.
	_, cmd, handled := handleKeyDown(m, keyMsgSpecial(tea.KeyDown))
	if !handled {
		t.Fatal("expected handled=true")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd (ScheduleFlashClear)")
	}
}

// ─── handleKeyUpHome (cursor at top, no items) ───────────────────────────────

// TestHandleKeyUpHome_CursorTopBoundary covers cursor>0 -> 0 transition.
func TestHandleKeyUpHome_CursorTopBoundary(t *testing.T) {
	m := newTestModel(nil)
	m.info.Port = 8181
	m.info.PasswordEphemeral = true
	m.info.Password = "swordfish"
	m.homeCursor = 0 // already at top
	newM, _, handled := handleKeyUpHome(m)
	if !handled {
		t.Fatal("expected handled=true")
	}
	if newM.homeCursor != 0 {
		t.Fatalf("expected homeCursor=0 (clamped at top), got %d", newM.homeCursor)
	}
}

// TestHandleKey_ExtraCmdWithUnhandled covers the `extraCmd != nil && !handled`
// branch in handleKey. Only handleKeyEnter takes this path (Sidebar focus with
// an open fullscreen exec view: closeExecView() shifts the mouse state and
// returns a non-nil EnableMouseAllMotion cmd alongside handled=false so the
// sidebar can also react to Enter).
func TestHandleKey_ExtraCmdWithUnhandled(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	// Toggle fullscreen + sync mouse state so the dialog manager records
	// mouseDisabled=true; the subsequent closeExecView then emits a non-nil
	// EnableMouseAllMotion cmd, satisfying `extraCmd != nil`.
	m.execView.ToggleFullscreen()
	if cmd := m.syncMouseState(); cmd != nil {
		_ = cmd()
	}
	m.panelFocus = uikit.PanelSidebar

	newM, cmd := m.handleKey(keyMsgSpecial(tea.KeyEnter))
	got, ok := newM.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
	if got.execView != nil {
		t.Fatal("expected execView cleared via closeExecView path")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from extraCmd batch (mouse re-enable)")
	}
}

// ─── canOpenLogSearch / openLogSearch / handleLogSearchKey / handleLogSearchSelect

func TestCanOpenLogSearch_NoClient(t *testing.T) {
	m := newTestModel(nil)
	if m.canOpenLogSearch() {
		t.Fatal("expected false when client is nil")
	}
}

func TestCanOpenLogSearch_ExecViewRun(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1"})
	m.execView = &ev
	if !m.canOpenLogSearch() {
		t.Fatal("expected true when execView has a run")
	}
}

func TestCanOpenLogSearch_SidebarTask(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "alpha"}}
	m := newTestModel(tasks)
	m.client = newDummyClient()
	selectSidebarItem(&m, 1)
	if m.sidebar.ActiveTask() == "" {
		t.Fatal("precondition: sidebar should report active task")
	}
	if !m.canOpenLogSearch() {
		t.Fatal("expected true when sidebar has active task")
	}
}

func TestCanOpenLogSearch_NoSelection(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	if m.canOpenLogSearch() {
		t.Fatal("expected false with no execView and no task selection")
	}
}

func TestOpenLogSearch_FromExecView(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "task-x"})
	m.execView = &ev

	newM, cmd := m.openLogSearch()
	if cmd != nil {
		t.Fatal("expected no cmd from openLogSearch")
	}
	got := newM.(Model)
	if got.logSearch == nil {
		t.Fatal("expected logSearch overlay attached")
	}
	if got.logSearch.TaskName() != "task-x" {
		t.Fatalf("expected scoped to task-x, got %q", got.logSearch.TaskName())
	}
}

func TestOpenLogSearch_FromSidebar(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "alpha"}}
	m := newTestModel(tasks)
	m.client = newDummyClient()
	selectSidebarItem(&m, 1)

	newM, _ := m.openLogSearch()
	got := newM.(Model)
	if got.logSearch == nil || got.logSearch.TaskName() != "alpha" {
		t.Fatalf("expected overlay scoped to alpha, got %#v", got.logSearch)
	}
}

func TestOpenLogSearch_NoTask_NoOverlay(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	newM, cmd := m.openLogSearch()
	if cmd != nil {
		t.Fatal("expected no cmd when no task")
	}
	got := newM.(Model)
	if got.logSearch != nil {
		t.Fatal("expected no overlay attached when no task is selected")
	}
}

func TestHandleLogSearchKey_EscClosesOverlay(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	ls := logsearch.New(m.client, "task-x")
	m.logSearch = &ls

	newM, cmd := m.handleLogSearchKey(keyMsgSpecial(tea.KeyEsc))
	if cmd != nil {
		t.Fatal("expected no cmd on esc")
	}
	got := newM.(Model)
	if got.logSearch != nil {
		t.Fatal("expected logSearch cleared on esc")
	}
}

func TestHandleLogSearchKey_ForwardsKey(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	ls := logsearch.New(m.client, "task-x")
	m.logSearch = &ls

	// Tab toggles regex inside the overlay; we don't observe the boolean
	// directly (unexported), but the overlay should still be attached and
	// no cmd should be returned for tab.
	newM, _ := m.handleLogSearchKey(tea.KeyMsg{Type: tea.KeyTab})
	got := newM.(Model)
	if got.logSearch == nil {
		t.Fatal("overlay should still be attached after Tab")
	}
	if !got.logSearch.Regex() {
		t.Fatal("expected regex toggled on after Tab")
	}
}

func TestHandleLogSearchSelect_OpensRunWhenNotAlreadyOpen(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	ls := logsearch.New(m.client, "task-x")
	m.logSearch = &ls

	newM, cmd := m.handleLogSearchSelect(logsearch.SelectMsg{TaskName: "task-x", RunID: "r-other", Line: 42})
	got := newM.(Model)
	if got.logSearch != nil {
		t.Fatal("expected overlay cleared")
	}
	if got.pendingHighlight != 42 {
		t.Fatalf("pendingHighlight: got %d want 42", got.pendingHighlight)
	}
	if cmd == nil {
		t.Fatal("expected openRunByID cmd")
	}
}

func TestHandleLogSearchSelect_JumpsInPlaceIfSameRunOpen(t *testing.T) {
	m := newTestModel(nil)
	m.client = newDummyClient()
	ls := logsearch.New(m.client, "task-x")
	m.logSearch = &ls
	ev := execlist.NewExecView(&model.Run{ID: "r-here", TaskName: "task-x"})
	m.execView = &ev

	newM, cmd := m.handleLogSearchSelect(logsearch.SelectMsg{TaskName: "task-x", RunID: "r-here", Line: 7})
	if cmd != nil {
		t.Fatal("expected no cmd when run is already open")
	}
	got := newM.(Model)
	if got.logSearch != nil {
		t.Fatal("expected overlay cleared")
	}
	if got.pendingHighlight != 0 {
		t.Fatalf("pendingHighlight should be cleared after in-place jump, got %d", got.pendingHighlight)
	}
	if got.execView.Pane.HighlightLine != 7 {
		t.Fatalf("expected pane highlight line to be 7, got %d", got.execView.Pane.HighlightLine)
	}
}
