// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/server/dto"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
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
	run := &dto.Run{ID: "run1", TaskName: "task1"}
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
	m.notifications.Toggle() // expand
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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	m.notifications.Toggle() // expand so Selected() returns the row

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
	run := &dto.Run{ID: "r1", TaskName: "t1"}
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
	reason := sqlcdb.ReasonFailed
	tests := []struct {
		name string
		make func() execlist.ExecView
	}{
		{
			"ActionStop (running, non-service)",
			func() execlist.ExecView {
				return execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseRunning})
			},
		},
		{
			"ActionStopService (running service)",
			func() execlist.ExecView {
				ev := execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseRunning})
				ev.TaskIsService = true
				return ev
			},
		},
		{
			"ActionRetry (ended, retryable)",
			func() execlist.ExecView {
				return execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseEnded, EndReason: &reason})
			},
		},
		{
			"ActionRestartService (service stopped)",
			func() execlist.ExecView {
				ev := execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1"})
				ev.TaskIsService = true
				ev.SetServiceStopped(true)
				return ev
			},
		},
		{
			"ActionDelete (ended, non-retryable, non-service)",
			func() execlist.ExecView {
				success := sqlcdb.ReasonSuccess
				return execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseEnded, EndReason: &success})
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
	reason := sqlcdb.ReasonFailed
	tests := []struct {
		name string
		make func() execlist.ExecView
	}{
		{
			"ActionStop (running, non-service)",
			func() execlist.ExecView {
				return execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseRunning})
			},
		},
		{
			"ActionStopService (running service)",
			func() execlist.ExecView {
				ev := execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseRunning})
				ev.TaskIsService = true
				return ev
			},
		},
		{
			"non-stop action (ended, retryable)",
			func() execlist.ExecView {
				return execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseEnded, EndReason: &reason})
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
	success := sqlcdb.ReasonSuccess
	ev := execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseEnded, EndReason: &success})
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
	ev := execlist.NewExecView(&dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseRunning})
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
	run := &dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.ToggleFullscreen() // set fullscreen
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
	run := &dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseRunning}
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
	run := &dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseRunning}
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
	m.notifications.Toggle() // expand
	_, _, handled := handleKeyR(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if !handled {
		t.Fatal("expected handled=true when notifications expanded")
	}
}

func TestHandleKeyR_CapitalR(t *testing.T) {
	m := newTestModel(nil)
	_, _, handled := handleKeyR(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if handled {
		t.Fatal("expected handled=false for capital R (delegates)")
	}
}

func TestHandleKeyR_WithExecViewRetry(t *testing.T) {
	m := newTestModel(nil)
	reason := sqlcdb.ReasonFailed
	run := &dto.Run{ID: "r1", TaskName: "t1", Status: sqlcdb.PhaseEnded, EndReason: &reason}
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
	m.notifications.Toggle() // expand
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
	m.notifications.Toggle() // expand
	_, _, handled := handleKeyDown(m, tea.KeyMsg{Type: tea.KeyDown})
	if !handled {
		t.Fatal("expected handled=true when notifications expanded")
	}
}
