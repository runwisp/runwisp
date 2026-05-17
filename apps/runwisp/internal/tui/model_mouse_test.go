// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	msg := tea.MouseMsg{Action: tea.MouseActionMotion, X: 50, Y: 5}
	newM, _ := m.handleMouse(msg)
	nm := newM.(Model)
	if nm.mouse.hoverX != 50 || nm.mouse.hoverY != 5 {
		t.Fatalf("expected hoverX=50, hoverY=5 after motion, got %d,%d", nm.mouse.hoverX, nm.mouse.hoverY)
	}
}
