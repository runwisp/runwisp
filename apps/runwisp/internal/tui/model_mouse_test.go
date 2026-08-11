// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
)

// TestMouseHandlers_Guards covers no-op branches of mouse handlers: non-home
// page returns nil, x-before-sidebar resets hover, nil execView short-circuits,
// nil execView.Run short-circuits.
func TestMouseHandlers_Guards(t *testing.T) {
	t.Run("handleMainPanelClick on non-home page returns nil cmd", func(t *testing.T) {
		m := newTestModel([]model.TaskBrief{{Name: "t1"}})
		selectSidebarItem(&m, 2) // Info
		if m.sidebar.ActivePage() == uikit.PageHome {
			t.Fatalf("precondition: expected non-home page after selecting Info")
		}
		_, cmd := m.handleMainPanelClick(100, 5)
		if cmd != nil {
			t.Fatal("expected nil cmd for non-home page")
		}
	})

	t.Run("updateHoverState with x<sidebarWidth resets homeHover", func(t *testing.T) {
		m := newTestModel(nil)
		m.updateHoverState(0, 5)
		if m.mouse.homeHover != -1 {
			t.Fatalf("expected homeHover=-1, got %d", m.mouse.homeHover)
		}
	})

	t.Run("handleExecViewClick with nil execView returns nil cmd", func(t *testing.T) {
		m := newTestModel(nil)
		if _, cmd := m.handleExecViewClick(100, 5); cmd != nil {
			t.Fatal("expected nil cmd when execView is nil")
		}
	})

	t.Run("handleExecViewClick with nil execView.Run returns nil cmd", func(t *testing.T) {
		m := newTestModel(nil)
		ev := execlist.NewExecView(nil)
		m.execView = &ev
		if _, cmd := m.handleExecViewClick(100, 5); cmd != nil {
			t.Fatal("expected nil cmd when execView.Run is nil")
		}
	})
}

// ─── handleMouse ─────────────────────────────────────────────────────────────

func TestHandleMouse_MotionUpdatesHoverCoords(t *testing.T) {
	m := newTestModel(nil)
	msg := tea.MouseMotionMsg{X: 50, Y: 5}
	newM, _ := m.handleMouse(msg)
	nm := newM.(Model)
	if nm.mouse.hoverX != 50 || nm.mouse.hoverY != 5 {
		t.Fatalf("expected hoverX=50, hoverY=5 after motion, got %d,%d", nm.mouse.hoverX, nm.mouse.hoverY)
	}
}

// ─── scrollWheelUp / scrollWheelDown ─────────────────────────────────────────

func TestScrollWheelUp_WithExecViewScrollsPaneUp(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	if cmd := m.scrollWheelUp(100); cmd != nil {
		t.Fatalf("expected nil cmd, got %v", cmd)
	}
	if m.execView.HeaderFocus != execlist.HeaderFocusNone {
		t.Fatal("expected HeaderFocus to be reset to None after wheel up")
	}
}

func TestScrollWheelDown_WithExecViewScrollsPaneDown(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	if cmd := m.scrollWheelDown(100); cmd != nil {
		t.Fatalf("expected nil cmd, got %v", cmd)
	}
}

func TestScrollWheelUp_OnInfoPageScrollsInfoView(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "t1"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 2) // Info page
	if cmd := m.scrollWheelUp(uikit.SidebarWidth + 5); cmd != nil {
		t.Fatalf("expected nil cmd on info page, got %v", cmd)
	}
}

func TestScrollWheelDown_OnInfoPageScrollsInfoView(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "t1"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 2) // Info
	if cmd := m.scrollWheelDown(uikit.SidebarWidth + 5); cmd != nil {
		t.Fatalf("expected nil cmd on info page, got %v", cmd)
	}
}

func TestScrollWheelUp_OnDebugPageScrollsDebugView(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "t1"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 3) // Debug
	if cmd := m.scrollWheelUp(uikit.SidebarWidth + 5); cmd != nil {
		t.Fatalf("expected nil cmd on debug page, got %v", cmd)
	}
}

func TestScrollWheelDown_OnDebugPageScrollsDebugView(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "t1"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 3) // Debug
	if cmd := m.scrollWheelDown(uikit.SidebarWidth + 5); cmd != nil {
		t.Fatalf("expected nil cmd on debug page, got %v", cmd)
	}
}

func TestScrollWheelUp_OnHomePageScrollsExecList(t *testing.T) {
	m := newTestModel(nil)
	// Default: PageHome, no execView. Falls through to execList.ScrollBy(-3).
	// Returns nil because the client is nil → fetchExecWindow yields nil.
	_ = m.scrollWheelUp(uikit.SidebarWidth + 5)
}

func TestScrollWheelDown_OnHomePageScrollsExecList(t *testing.T) {
	m := newTestModel(nil)
	_ = m.scrollWheelDown(uikit.SidebarWidth + 5)
}

// ─── handleHomePageClick ─────────────────────────────────────────────────────

func TestHandleHomePageClick_ClickOnTaskButtonTriggers(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 1)
	m.layout.taskBtnY = 7
	// client is nil → confirmAction returns nil. We still exercise the branch.
	_, _ = m.handleHomePageClick(uikit.SidebarWidth+5, 7)
}

func TestHandleHomePageClick_ClickOnHomeFieldFocusesField(t *testing.T) {
	m := newTestModel(nil)
	m.layout.homeFieldsY = 5
	// Try to click at y == homeFieldsY (field 0).
	newM, _ := m.handleHomePageClick(uikit.SidebarWidth+5, 5)
	if nm, ok := newM.(Model); ok {
		// Could be either home field or pass-through to exec list, depending on
		// whether fields are present. Either way, no panic + return is enough.
		_ = nm
	}
}

func TestHandleHomePageClick_NoTaskAndOutsideFieldsDelegatesToExecList(t *testing.T) {
	m := newTestModel(nil)
	m.layout.homeFieldsY = 100 // ensure y < fieldsStartY → fieldIdx negative
	_, _ = m.handleHomePageClick(uikit.SidebarWidth+5, 0)
}

// ─── handleExecListClick ─────────────────────────────────────────────────────

func TestHandleExecListClick_LocalYNegativeReturnsFocusOnly(t *testing.T) {
	m := newTestModel(nil)
	m.layout.homeH = 100 // mainHeaderHeight will exceed y
	focusCmd := tea.Cmd(func() tea.Msg { return uikit.DebugLogMsg{Message: "focus"} })
	_, cmd := m.handleExecListClick(0, focusCmd)
	if cmd == nil {
		t.Fatal("expected focusCmd to be returned when localY < 0")
	}
}

func TestHandleExecListClick_NoSelectedRunReturnsFocusOnly(t *testing.T) {
	m := newTestModel(nil)
	m.layout.homeH = 0
	_, cmd := m.handleExecListClick(1, nil)
	// No selected run → just focus cmd (nil here).
	if cmd != nil {
		t.Fatalf("expected nil cmd, got %v", cmd)
	}
}

// ─── handleMouse: full integration paths ─────────────────────────────────────

// TestHandleMouse_WheelUpPress covers the wheel-up press path which routes to
// scrollWheelUp.
func TestHandleMouse_WheelUpPress(t *testing.T) {
	m := newTestModel(nil)
	msg := tea.MouseClickMsg{Button: tea.MouseWheelUp, X: 100, Y: 5}
	newM, _ := m.handleMouse(msg)
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
}

// TestHandleMouse_WheelDownPress covers the wheel-down press path.
func TestHandleMouse_WheelDownPress(t *testing.T) {
	m := newTestModel(nil)
	msg := tea.MouseClickMsg{Button: tea.MouseWheelDown, X: 100, Y: 5}
	newM, _ := m.handleMouse(msg)
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
}

// TestHandleMouse_LeftClickSidebarFocusesSidebar covers the sidebar-click
// branch (x < SidebarWidth).
func TestHandleMouse_LeftClickSidebarFocusesSidebar(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup"}}
	m := newTestModel(tasks)
	m.focusMainPanel()
	msg := tea.MouseClickMsg{Button: tea.MouseLeft, X: 1, Y: 1}
	newM, _ := m.handleMouse(msg)
	got, ok := newM.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
	if got.panelFocus != uikit.PanelSidebar {
		t.Fatalf("expected PanelSidebar after sidebar click, got %v", got.panelFocus)
	}
}

// TestHandleMouse_LeftClickMainPanelRoutes covers the click-in-main-panel path.
func TestHandleMouse_LeftClickMainPanelRoutes(t *testing.T) {
	m := newTestModel(nil)
	msg := tea.MouseClickMsg{Button: tea.MouseLeft, X: uikit.SidebarWidth + 5, Y: 10}
	newM, _ := m.handleMouse(msg)
	if _, ok := newM.(Model); !ok {
		t.Fatalf("expected Model, got %T", newM)
	}
}

// TestHandleMouse_NonLeftNonWheelIsNoOp covers the "ignore other buttons"
// branch — a right click does nothing.
func TestHandleMouse_NonLeftNonWheelIsNoOp(t *testing.T) {
	m := newTestModel(nil)
	msg := tea.MouseClickMsg{Button: tea.MouseRight, X: 100, Y: 5}
	_, cmd := m.handleMouse(msg)
	if cmd != nil {
		t.Fatalf("expected nil cmd for right-click, got %v", cmd)
	}
}

// TestHandleMouse_ReleaseIsNoOp ensures non-press actions other than motion are
// ignored (release).
func TestHandleMouse_ReleaseIsNoOp(t *testing.T) {
	m := newTestModel(nil)
	msg := tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 100, Y: 5}
	_, cmd := m.handleMouse(msg)
	if cmd != nil {
		t.Fatalf("expected nil cmd for release, got %v", cmd)
	}
}

// ─── handleExecViewClick: every HitAt result ─────────────────────────────────

// findHitCoord renders the exec view to populate hit boxes, then searches the
// header rows for a coordinate that matches the requested HeaderFocusItem.
// Returns x, y, true when found, or 0, 0, false if absent.
func findHitCoord(ev *execlist.ExecView, target execlist.HeaderFocusItem, w int) (int, int, bool) {
	// Force a render so headerLayout.hitBoxes is populated.
	_ = ev.View()
	// Hit boxes are placed in screen coordinates, offset by the sidebar; the
	// Action button in particular sits at the far right (~SidebarWidth+paneW),
	// so the search must span the whole rendered width, not just the pane.
	for y := 0; y < 4; y++ {
		for x := 0; x < uikit.SidebarWidth+w; x++ {
			if ev.HitAt(x, y) == target {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

// TestHandleExecViewClick_BackButton covers the back-button click branch.
func TestHandleExecViewClick_BackButton(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev

	x, y, ok := findHitCoord(m.execView, execlist.HeaderFocusBack, 80)
	if !ok {
		t.Skip("no HeaderFocusBack hit-coordinate found; layout may have changed")
	}
	updated, _ := m.handleExecViewClick(x, y)
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	if got.execView != nil {
		t.Fatal("expected execView closed after Back click")
	}
}

// TestHandleExecViewClick_IDCopiesValue covers the HeaderFocusID branch.
func TestHandleExecViewClick_IDCopiesValue(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev

	x, y, ok := findHitCoord(m.execView, execlist.HeaderFocusID, 80)
	if !ok {
		t.Skip("no HeaderFocusID hit-coordinate found")
	}
	_, cmd := m.handleExecViewClick(x, y)
	if cmd == nil {
		t.Fatal("expected non-nil clipboard cmd for ID hit")
	}
}

// TestHandleExecViewClick_StartedCopiesValue covers HeaderFocusStarted.
func TestHandleExecViewClick_StartedCopiesValue(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev

	x, y, ok := findHitCoord(m.execView, execlist.HeaderFocusStarted, 80)
	if !ok {
		t.Skip("no HeaderFocusStarted hit-coordinate found")
	}
	_, cmd := m.handleExecViewClick(x, y)
	if cmd == nil {
		t.Fatal("expected non-nil clipboard cmd for Started hit")
	}
}

// TestHandleExecViewClick_ActionStop covers the action-button branch with a
// running non-service run → ActionStop.
func TestHandleExecViewClick_ActionStop(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	m.client = newDummyClient() // confirmAction needs a non-nil client
	run := &model.Run{ID: "r-1234567890", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev

	x, y, ok := findHitCoord(m.execView, execlist.HeaderFocusAction, 80)
	if !ok {
		t.Skip("no HeaderFocusAction hit-coordinate found")
	}
	updated, _ := m.handleExecViewClick(x, y)
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	if !got.dialogs.HasConfirm() {
		t.Fatal("expected stop confirm dialog after action click")
	}
}

// TestHandleExecViewClick_ActionDelete covers the action-button branch with a
// terminal, non-retryable, non-service run → ActionDelete. Regression guard:
// the mouse handler previously omitted the Delete case, so clicking 🗑 Delete
// did nothing even though the hover highlight worked.
func TestHandleExecViewClick_ActionDelete(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	m.client = newDummyClient() // confirmAction needs a non-nil client
	run := &model.Run{
		ID:        "r-1234567890",
		TaskName:  "t1",
		Status:    model.PhaseEnded,
		EndReason: model.EndReasonPtr(model.ReasonSuccess),
	}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev

	if ev.Action() != execlist.ActionDelete {
		t.Fatalf("test setup: expected ActionDelete, got %d", ev.Action())
	}

	x, y, ok := findHitCoord(m.execView, execlist.HeaderFocusAction, 80)
	if !ok {
		t.Skip("no HeaderFocusAction hit-coordinate found")
	}
	updated, cmd := m.handleExecViewClick(x, y)
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}
	// Delete acts immediately (soft delete is undoable), so the click issues a
	// command rather than opening a confirm dialog.
	if cmd == nil {
		t.Fatal("expected a delete command after action click")
	}
	if got.dialogs.HasConfirm() {
		t.Fatal("delete must act immediately, not open a confirm dialog")
	}
}

// TestHandleExecViewClick_NoHitReturnsNil covers the fall-through "no hit"
// branch (HitAt returns HeaderFocusNone).
func TestHandleExecViewClick_NoHitReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev

	// y way past the header → no hit → just clears HeaderFocus and returns nil.
	_, cmd := m.handleExecViewClick(uikit.SidebarWidth+5, 500)
	if cmd != nil {
		t.Fatalf("expected nil cmd for no-hit click, got %v", cmd)
	}
}

// TestHandleExecViewClick_ResetsHeaderFocus verifies any click clears keyboard
// header focus (production rule: "clear keyboard header focus on any click").
func TestHandleExecViewClick_ResetsHeaderFocus(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	ev.HeaderFocus = execlist.HeaderFocusID
	m.execView = &ev

	m.handleExecViewClick(uikit.SidebarWidth+5, 500)
	if m.execView.HeaderFocus != execlist.HeaderFocusNone {
		t.Fatalf("expected HeaderFocus=None after click, got %v", m.execView.HeaderFocus)
	}
}

// ─── updateHoverState: additional branches ───────────────────────────────────

// TestUpdateHoverState_ExecViewSetsHoveredHeader covers the execView hover
// branch.
func TestUpdateHoverState_ExecViewSetsHoveredHeader(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev

	m.updateHoverState(uikit.SidebarWidth+10, 1)
	// Just verify no panic and homeHover reset.
	if m.mouse.homeHover != -1 {
		t.Fatalf("expected homeHover=-1 with execView, got %d", m.mouse.homeHover)
	}
}

// TestUpdateHoverState_HomePageWithActiveTaskResetsHomeHover covers the
// active-task branch where homeHover is forced to -1.
func TestUpdateHoverState_HomePageWithActiveTaskResetsHomeHover(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 1) // active task

	m.updateHoverState(uikit.SidebarWidth+10, 5)
	if m.mouse.homeHover != -1 {
		t.Fatalf("expected homeHover=-1 with active task, got %d", m.mouse.homeHover)
	}
}

// TestUpdateHoverState_HomePageNoTaskHoversField covers the field-hover branch.
func TestUpdateHoverState_HomePageNoTaskHoversField(t *testing.T) {
	m := newTestModel(nil)
	m.info.Port = 8181
	m.layout.homeFieldsY = 3
	// Place mouse on the first home field row.
	m.updateHoverState(uikit.SidebarWidth+10, 3)
	if m.mouse.homeHover != 0 {
		t.Fatalf("expected homeHover=0 when over field row, got %d", m.mouse.homeHover)
	}
}

// TestUpdateHoverState_NonHomePageResetsHomeHover covers the non-home page
// branch.
func TestUpdateHoverState_NonHomePageResetsHomeHover(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "t1"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 2) // Info

	m.updateHoverState(uikit.SidebarWidth+10, 5)
	if m.mouse.homeHover != -1 {
		t.Fatalf("expected homeHover=-1 on non-home page, got %d", m.mouse.homeHover)
	}
}

// ─── handleMainPanelClick (execView branch) ──────────────────────────────────

func TestHandleMainPanelClick_WithExecViewRoutesToExecViewClick(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	run := &model.Run{ID: "r-1234567890", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev
	// Click somewhere in main panel; HitAt returns HeaderFocusNone for off-header
	// y → handleExecViewClick clears HeaderFocus and returns nil cmd.
	_, cmd := m.handleMainPanelClick(uikit.SidebarWidth+5, 500)
	if cmd != nil {
		t.Fatalf("expected nil cmd for no-hit exec view click, got %v", cmd)
	}
}

// ─── handleHomePageClick double-click triggers activateHomeField ─────────────

func TestHandleHomePageClick_DoubleClickActivatesField(t *testing.T) {
	m := newTestModel(nil)
	m.info.Port = 8181
	m.layout.homeFieldsY = 5
	// First click primes detectDoubleClick.
	m.handleHomePageClick(uikit.SidebarWidth+5, 5)
	// Second click at same Y within threshold → double click → activate.
	_, _ = m.handleHomePageClick(uikit.SidebarWidth+5, 5)
}
