// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
)

// newTestModel builds a minimal Model suitable for unit-testing helper methods
// that don't touch the network or real storage. A nil client is fine for these
// tests.
func newTestModel(tasks []model.TaskBrief) Model {
	return NewModel(TUIConfig{
		Info: uikit.StartupInfo{
			Version: "0.0.0-test",
			Tasks:   tasks,
		},
	})
}

// selectSidebarItem drives the sidebar keyboard navigation to place the
// selection at the given zero-based item index by pressing "j" (index) times
// then Enter.
func selectSidebarItem(m *Model, index int) {
	for i := 0; i < index; i++ {
		m.sidebar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	m.sidebar.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

// testNotif is a minimal unread notification for seeding the panel.
func testNotif(id string) server.NotificationDTO {
	return server.NotificationDTO{
		ID:             id,
		Severity:       "info",
		Count:          1,
		LastOccurredAt: time.Now(),
		Title:          id,
	}
}

// ─── mainHeaderHeight ────────────────────────────────────────────────────────

// TestMainHeaderHeight_HomeNoTask verifies that on the Home page with no
// active task the result is homeH + notifications.PanelHeight().
func TestMainHeaderHeight_HomeNoTask(t *testing.T) {
	m := newTestModel(nil)

	if m.sidebar.ActivePage() != uikit.PageHome {
		t.Fatalf("expected PageHome, got %v", m.sidebar.ActivePage())
	}
	if m.sidebar.ActiveTask() != "" {
		t.Fatalf("expected empty active task, got %q", m.sidebar.ActiveTask())
	}

	m.layout.homeH = 5
	m.layout.taskH = 99 // must not contribute
	// notifications panel is empty → PanelHeight() == 0
	got := m.mainHeaderHeight()
	want := 5 // homeH + 0
	if got != want {
		t.Fatalf("mainHeaderHeight (home, empty notifications): want %d, got %d", want, got)
	}
}

// TestMainHeaderHeight_HomeWithNotifications verifies that a non-zero
// notifications panel height is added when on the Home page.
func TestMainHeaderHeight_HomeWithNotifications(t *testing.T) {
	m := newTestModel(nil)
	m.layout.homeH = 3

	// Seed a notification so the panel reserves collapsed height (1 line).
	m.notifications.Upsert(testNotif("n1"))
	panelH := m.notifications.PanelHeight()
	if panelH == 0 {
		t.Fatal("expected non-zero panel height after upsert")
	}

	got := m.mainHeaderHeight()
	want := 3 + panelH
	if got != want {
		t.Fatalf("mainHeaderHeight (home, with notifications): want %d, got %d", want, got)
	}
}

// TestMainHeaderHeight_ActiveTask verifies that taskH (not homeH) is used as
// the base when a task is active. Notifications are still included because
// task items live on PageHome.
func TestMainHeaderHeight_ActiveTask(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup-db"}}
	m := newTestModel(tasks)
	// Sidebar items: [Home(0), backup-db(1), Info(2), Debug(3)]
	selectSidebarItem(&m, 1) // select "backup-db"

	if m.sidebar.ActiveTask() != "backup-db" {
		t.Fatalf("expected active task backup-db, got %q", m.sidebar.ActiveTask())
	}
	if m.sidebar.ActivePage() != uikit.PageHome {
		t.Fatalf("expected PageHome for task entry, got %v", m.sidebar.ActivePage())
	}

	m.layout.homeH = 99 // must not be used
	m.layout.taskH = 7
	panelH := m.notifications.PanelHeight() // empty → 0

	got := m.mainHeaderHeight()
	want := 7 + panelH
	if got != want {
		t.Fatalf("mainHeaderHeight (active task): want %d, got %d", want, got)
	}
}

// TestMainHeaderHeight_NonHomePage verifies that notifications.PanelHeight()
// is NOT added when the active page is not PageHome.
func TestMainHeaderHeight_NonHomePage(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup-db"}}
	m := newTestModel(tasks)
	// Sidebar items: [Home(0), backup-db(1), Info(2), Debug(3)]
	selectSidebarItem(&m, 2) // select "Info"

	if m.sidebar.ActivePage() == uikit.PageHome {
		t.Fatalf("expected non-home page after selecting Info, got PageHome")
	}

	m.layout.homeH = 4
	m.layout.taskH = 99 // no active task on Info page
	// Seed a notification so PanelHeight > 0 — it must NOT be counted here.
	m.notifications.Upsert(testNotif("n2"))

	got := m.mainHeaderHeight()
	// No active task → base = homeH; page != PageHome → no panel height added.
	want := 4
	if got != want {
		t.Fatalf("mainHeaderHeight (info page, no panel): want %d, got %d", want, got)
	}
}

// ─── detectDoubleClick ───────────────────────────────────────────────────────

// TestDetectDoubleClick_FirstClickIsFalse verifies the very first click never
// registers as a double-click.
func TestDetectDoubleClick_FirstClickIsFalse(t *testing.T) {
	m := newTestModel(nil)
	if m.detectDoubleClick(5) {
		t.Fatal("first click must not be a double-click")
	}
}

// TestDetectDoubleClick_SameYWithinThreshold verifies two rapid clicks at the
// same Y coordinate are detected as a double-click.
func TestDetectDoubleClick_SameYWithinThreshold(t *testing.T) {
	m := newTestModel(nil)
	m.detectDoubleClick(5) // prime: sets lastClickY=5, lastClickTime≈now
	if !m.detectDoubleClick(5) {
		t.Fatal("second click at same Y within threshold must be a double-click")
	}
}

// TestDetectDoubleClick_DifferentYIsNotDouble verifies that clicks at different
// Y values are not treated as a double-click even when rapid.
func TestDetectDoubleClick_DifferentYIsNotDouble(t *testing.T) {
	m := newTestModel(nil)
	m.detectDoubleClick(5)
	if m.detectDoubleClick(6) {
		t.Fatal("click at a different Y must not be a double-click")
	}
}

// TestDetectDoubleClick_ExpiredThreshold verifies that a second click at the
// same Y after the 400 ms window has elapsed is not a double-click.
func TestDetectDoubleClick_ExpiredThreshold(t *testing.T) {
	m := newTestModel(nil)
	m.detectDoubleClick(5) // prime

	// Back-date lastClickTime so the threshold window is already past.
	m.mouse.lastClickTime = time.Now().Add(-(doubleClickThreshold + time.Millisecond))

	if m.detectDoubleClick(5) {
		t.Fatal("click after threshold expiry must not be a double-click")
	}
}

// ─── focusSidebar ────────────────────────────────────────────────────────────

func TestFocusSidebar_SetsPanelFocusAndSidebarFocused(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel() // start on main panel
	if m.panelFocus == uikit.PanelSidebar {
		t.Fatal("precondition: expected PanelMain initially after focusMainPanel")
	}

	m.focusSidebar()

	if m.panelFocus != uikit.PanelSidebar {
		t.Fatalf("expected PanelSidebar, got %v", m.panelFocus)
	}
	if m.homeCursor != -1 {
		t.Fatalf("expected homeCursor=-1 after focusSidebar, got %d", m.homeCursor)
	}
}

// ─── focusMainPanel ──────────────────────────────────────────────────────────

func TestFocusMainPanel_SetsPanelFocusAndSidebarUnfocused(t *testing.T) {
	m := newTestModel(nil)
	// default: PanelSidebar

	m.focusMainPanel()

	if m.panelFocus != uikit.PanelMain {
		t.Fatalf("expected PanelMain, got %v", m.panelFocus)
	}
	if m.homeCursor != -1 {
		t.Fatalf("expected homeCursor=-1, got %d", m.homeCursor)
	}
}

// ─── focusHomeField ──────────────────────────────────────────────────────────

func TestFocusHomeField_SetsHomeCursor(t *testing.T) {
	m := newTestModel(nil)

	m.focusHomeField(2)

	if m.panelFocus != uikit.PanelMain {
		t.Fatalf("expected PanelMain, got %v", m.panelFocus)
	}
	if m.homeCursor != 2 {
		t.Fatalf("expected homeCursor=2, got %d", m.homeCursor)
	}
}

// ─── confirm* helpers: nil-guard + happy-path coverage ────────────────────────

// TestConfirmHelpers_Guards covers the early-return branches of every confirm*
// helper (nil client / empty task / nil-or-non-running run) and the happy path
// where a task name is resolved and showConfirmDialog returns a non-nil cmd.
func TestConfirmHelpers_Guards(t *testing.T) {
	svc := []model.TaskBrief{{Name: "svc", Kind: model.KindService}}

	t.Run("confirmAction with nil client returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if cmd := m.confirmAction(confirmActionTrigger); cmd != nil {
			t.Fatal("expected nil cmd when client is nil")
		}
	})

	t.Run("confirmRestartService with empty task returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if cmd := m.confirmRestartService(); cmd != nil {
			t.Fatal("expected nil cmd when task name is empty")
		}
	})

	t.Run("confirmRestartService with task opens dialog", func(t *testing.T) {
		m := newTestModel(svc)
		selectSidebarItem(&m, 1)
		if cmd := m.confirmRestartService(); cmd != nil {
			t.Fatalf("showConfirmDialog returns nil itself, got %v", cmd)
		}
		if !m.dialogs.HasConfirm() {
			t.Fatal("expected a confirm dialog to be queued")
		}
	})

	t.Run("confirmStopService with empty task returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if cmd := m.confirmStopService(); cmd != nil {
			t.Fatal("expected nil cmd when task name is empty")
		}
	})

	t.Run("confirmStopService with task opens dialog", func(t *testing.T) {
		m := newTestModel(svc)
		selectSidebarItem(&m, 1)
		m.confirmStopService()
		if !m.dialogs.HasConfirm() {
			t.Fatal("expected a confirm dialog to be queued")
		}
	})

	t.Run("confirmStop with nil execView returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if cmd := m.confirmStop(); cmd != nil {
			t.Fatal("expected nil cmd when execView is nil")
		}
	})

	t.Run("confirmStop with non-running run returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded}
		ev := execlist.NewExecView(run)
		m.execView = &ev
		if cmd := m.confirmStop(); cmd != nil {
			t.Fatal("expected nil cmd for non-running run")
		}
	})

	t.Run("confirmRetry with nil execView returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if cmd := m.confirmRetry(); cmd != nil {
			t.Fatal("expected nil cmd when execView is nil")
		}
	})
}

// TestBuildMainHelpText pins that buildMainHelpText returns a non-empty string
// across the page/cursor/active-task branches it considers.
func TestBuildMainHelpText(t *testing.T) {
	t.Run("home page, sidebar focused, contains navigation hint", func(t *testing.T) {
		m := newTestModel(nil)
		text := m.buildMainHelpText()
		if text == "" || !strings.Contains(text, "↑↓") {
			t.Fatalf("expected non-empty text with navigation hint, got %q", text)
		}
	})

	t.Run("home page, home field focused (cursor >= 0)", func(t *testing.T) {
		m := newTestModel(nil)
		m.focusHomeField(0)
		if m.buildMainHelpText() == "" {
			t.Fatal("expected non-empty help text for homeCursor>=0")
		}
	})

	t.Run("info page", func(t *testing.T) {
		m := newTestModel([]model.TaskBrief{{Name: "t1"}})
		selectSidebarItem(&m, 2)
		if m.buildMainHelpText() == "" {
			t.Fatal("expected non-empty help text for PageInfo")
		}
	})

	t.Run("active service task", func(t *testing.T) {
		m := newTestModel([]model.TaskBrief{{Name: "svc", Kind: model.KindService}})
		selectSidebarItem(&m, 1)
		if m.buildMainHelpText() == "" {
			t.Fatal("expected non-empty help text with active service task")
		}
	})
}
