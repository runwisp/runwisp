// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
)

func TestView_NotReadyShowsInitializing(t *testing.T) {
	m := newTestModel(nil)
	// Default ready is false until handleWindowSize fires.
	if got := m.View(); got != "Initializing..." {
		t.Fatalf("not-ready View() = %q", got)
	}
}

func TestView_ReadyRendersBodyAndHelpBar(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	got := m.View()
	if got == "" {
		t.Fatal("expected non-empty View output")
	}
	// Help bar text comes from buildHelpText — sidebar focus default includes
	// the navigation hint.
	if !strings.Contains(got, "navigate") {
		t.Fatalf("expected help-bar hint in View output, got: %q", got)
	}
}

func TestHelpTextWithFlash_NoFlashReturnsHelp(t *testing.T) {
	m := newTestModel(nil)
	got := m.helpTextWithFlash()
	if !strings.Contains(got, "navigate") {
		t.Fatalf("expected base help text, got: %q", got)
	}
}

func TestHelpTextWithFlash_PrependsActiveFlash(t *testing.T) {
	m := newTestModel(nil)
	// Flash is applied through DialogManager — set one with a long TTL so it
	// is still "active" when helpTextWithFlash queries.
	m.dialogs.Flash("Saved", 5*time.Second)
	got := m.helpTextWithFlash()
	if !strings.Contains(got, "Saved") {
		t.Fatalf("expected flash text in output, got: %q", got)
	}
}

func TestRenderBody_DefaultSidebarPlusMain(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	// Default: no execView → renderBody joins sidebar and main horizontally.
	body := m.renderBody()
	if body == "" {
		t.Fatal("expected non-empty body")
	}
}

func TestRenderBody_FullscreenExecViewTrimsTrailingNewline(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.ToggleFullscreen()
	m.execView = &ev
	body := m.renderBody()
	if strings.HasSuffix(body, "\n") {
		t.Fatal("renderBody must trim trailing newline in fullscreen path")
	}
}

func TestRenderMainContent_PageInfoUsesInfoView(t *testing.T) {
	m := newTestModel([]model.TaskBrief{{Name: "t1"}})
	m, _ = m.applyWindowSize(120, 30)
	// Sidebar items: [Home(0), t1(1), Info(2), Debug(3)] — pick Info.
	selectSidebarItem(&m, 2)

	main := m.renderMainContent()
	if main == "" {
		t.Fatal("expected non-empty Info page content")
	}
}

func TestRenderMainContent_PageDebugUsesDebugView(t *testing.T) {
	m := newTestModel([]model.TaskBrief{{Name: "t1"}})
	m, _ = m.applyWindowSize(120, 30)
	// Item index 3 = Debug.
	selectSidebarItem(&m, 3)
	m.debugView.AppendLine("hello debug")
	main := m.renderMainContent()
	if !strings.Contains(main, "hello debug") {
		t.Fatalf("debug view content not surfaced, got: %q", main)
	}
}

func TestBuildHelpText_NotificationsExpandedHint(t *testing.T) {
	m := newTestModel(nil)
	m.notifications.Upsert(testNotif("n1"))
	m.notifications.Toggle()
	got := m.buildHelpText()
	if !strings.Contains(got, "mark read") {
		t.Fatalf("expanded-notifications hint missing: %q", got)
	}
}

func TestBuildHelpText_ExecViewHintIncludesQuit(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	got := m.buildHelpText()
	if !strings.Contains(got, "q/^C quit") {
		t.Fatalf("expected quit hint in exec-view help, got: %q", got)
	}
}

func TestBuildSidebarHelpText_ServiceShowsRestart(t *testing.T) {
	m := newTestModel([]model.TaskBrief{{Name: "svc", Kind: model.KindService}})
	// Place the cursor on the service entry (item 1) without pressing Enter so
	// CursorTaskName returns the service.
	m.sidebar.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := m.buildSidebarHelpText()
	if !strings.Contains(got, "restart") {
		t.Fatalf("expected restart hint for service, got: %q", got)
	}
}

func TestBuildSidebarHelpText_NoCursorTaskOmitsActionHint(t *testing.T) {
	m := newTestModel(nil)
	got := m.buildSidebarHelpText()
	if strings.Contains(got, " r ") {
		t.Fatalf("expected no action hint when cursor isn't on a task, got: %q", got)
	}
}

// applyWindowSize is a helper that drives handleWindowSize so the model marks
// itself ready and the layout is computed.
func (m Model) applyWindowSize(w, h int) (Model, tea.Cmd) {
	got, cmd := m.handleWindowSize(tea.WindowSizeMsg{Width: w, Height: h})
	return got.(Model), cmd
}

// ─── renderHomeContent ───────────────────────────────────────────────────────

// TestRenderHomeContent_NoActiveTaskRendersHomeHeader covers the no-active-task
// branch which calls home.RenderHeader and includes the exec list view.
func TestRenderHomeContent_NoActiveTaskRendersHomeHeader(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	got := m.renderHomeContent(80, "")
	if got == "" {
		t.Fatal("expected non-empty home content with no active task")
	}
}

// TestRenderHomeContent_ActiveTaskRendersTaskHeader covers the active-task
// branch which calls home.RenderTaskHeader.
func TestRenderHomeContent_ActiveTaskRendersTaskHeader(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup"}}
	m := newTestModel(tasks)
	m, _ = m.applyWindowSize(120, 30)
	selectSidebarItem(&m, 1) // pick "backup" as active task
	got := m.renderHomeContent(80, "")
	if got == "" {
		t.Fatal("expected non-empty content for active task")
	}
}

// TestRenderHomeContent_WithPanelView verifies the panel-view is prepended to
// the rendered content.
func TestRenderHomeContent_WithPanelView(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	got := m.renderHomeContent(80, "PANEL\n")
	if !strings.Contains(got, "PANEL") {
		t.Fatalf("expected panel view in output, got: %q", got)
	}
}

// ─── renderMainContent ───────────────────────────────────────────────────────

// TestRenderMainContent_WithExecViewRoutesToExecView verifies the execView
// branch of renderMainContent.
func TestRenderMainContent_WithExecViewRoutesToExecView(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	// ID must be >= 8 chars; the renderer slices Run.ID[-8:].
	run := &model.Run{ID: "run-12345678", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	m.execView = &ev
	got := m.renderMainContent()
	if got == "" {
		t.Fatal("expected non-empty exec view render")
	}
}

// TestRenderMainContent_HomeWithNotificationsPrependsPanelView covers the
// notifications-panel-height>0 branch when on PageHome.
func TestRenderMainContent_HomeWithNotificationsPrependsPanelView(t *testing.T) {
	m := newTestModel(nil)
	m, _ = m.applyWindowSize(120, 30)
	m.notifications.Upsert(testNotif("n1"))
	if m.notifications.PanelHeight() == 0 {
		t.Fatal("precondition: expected non-zero panel height")
	}
	got := m.renderMainContent()
	if got == "" {
		t.Fatal("expected non-empty content with notifications")
	}
}

// ─── buildHelpText branches ───────────────────────────────────────────────────

// TestBuildHelpText_SidebarFocusedRoutes covers the sidebar-focused branch.
func TestBuildHelpText_SidebarFocusedRoutes(t *testing.T) {
	m := newTestModel(nil)
	// default: PanelSidebar, no execView, no expanded notifications.
	got := m.buildHelpText()
	if !strings.Contains(got, "sidebar") && !strings.Contains(got, "navigate") {
		t.Fatalf("expected sidebar help text, got: %q", got)
	}
}

// TestBuildHelpText_MainPanelRoutes covers the main-help-text branch.
func TestBuildHelpText_MainPanelRoutes(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	got := m.buildHelpText()
	if got == "" {
		t.Fatal("expected non-empty main help text")
	}
}

// ─── buildExecViewHelpText ────────────────────────────────────────────────────

// TestBuildExecViewHelpText_FullscreenIncludesScroll covers the fullscreen
// branch of the exec-view help text builder.
func TestBuildExecViewHelpText_FullscreenIncludesScroll(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.ToggleFullscreen()
	m.execView = &ev
	got := m.buildExecViewHelpText()
	if !strings.Contains(got, "exit fullscreen") {
		t.Fatalf("expected exit-fullscreen hint, got: %q", got)
	}
}

// TestBuildExecViewHelpText_HeaderFocusBack covers the HeaderFocusBack case.
func TestBuildExecViewHelpText_HeaderFocusBack(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusBack
	m.execView = &ev
	got := m.buildExecViewHelpText()
	if !strings.Contains(got, "activate") {
		t.Fatalf("expected activate hint for HeaderFocusBack, got: %q", got)
	}
}

// TestBuildExecViewHelpText_HeaderFocusID covers the HeaderFocusID case.
func TestBuildExecViewHelpText_HeaderFocusID(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusID
	m.execView = &ev
	got := m.buildExecViewHelpText()
	if !strings.Contains(got, "copy") {
		t.Fatalf("expected copy hint for HeaderFocusID, got: %q", got)
	}
}

// TestBuildExecViewHelpText_HeaderFocusStarted covers the HeaderFocusStarted
// case (also covers HeaderFocusDuration via the shared branch).
func TestBuildExecViewHelpText_HeaderFocusStarted(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusStarted
	m.execView = &ev
	got := m.buildExecViewHelpText()
	if !strings.Contains(got, "buttons") {
		t.Fatalf("expected buttons hint for HeaderFocusStarted, got: %q", got)
	}
}

// TestBuildExecViewHelpText_HeaderFocusNone covers the default scroll branch.
func TestBuildExecViewHelpText_HeaderFocusNone(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	ev.HeaderFocus = execlist.HeaderFocusNone
	m.execView = &ev
	got := m.buildExecViewHelpText()
	if !strings.Contains(got, "fullscreen") {
		t.Fatalf("expected fullscreen hint for default scroll branch, got: %q", got)
	}
}

// ─── appendExecViewActionHints ────────────────────────────────────────────────

// TestAppendExecViewActionHints_AllActionsAndExtras covers the four Action
// branches plus the launch-ticket and CanDelete extras.
func TestAppendExecViewActionHints_AllActionsAndExtras(t *testing.T) {
	reason := model.ReasonFailed

	t.Run("ActionStop", func(t *testing.T) {
		m := newTestModel(nil)
		ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning})
		m.execView = &ev
		parts := m.appendExecViewActionHints(nil)
		var joined string
		for _, p := range parts {
			joined += p + " "
		}
		if !strings.Contains(joined, "stop") {
			t.Fatalf("expected stop hint, got: %v", parts)
		}
	})

	t.Run("ActionStopService", func(t *testing.T) {
		m := newTestModel(nil)
		ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning})
		ev.TaskIsService = true
		m.execView = &ev
		parts := m.appendExecViewActionHints(nil)
		var joined string
		for _, p := range parts {
			joined += p + " "
		}
		if !strings.Contains(joined, "stop service") {
			t.Fatalf("expected stop service hint, got: %v", parts)
		}
	})

	t.Run("ActionRetry", func(t *testing.T) {
		m := newTestModel(nil)
		ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &reason})
		m.execView = &ev
		parts := m.appendExecViewActionHints(nil)
		var joined string
		for _, p := range parts {
			joined += p + " "
		}
		if !strings.Contains(joined, "retry") {
			t.Fatalf("expected retry hint, got: %v", parts)
		}
	})

	t.Run("ActionRestartService", func(t *testing.T) {
		m := newTestModel(nil)
		ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1"})
		ev.TaskIsService = true
		ev.SetServiceStopped(true)
		m.execView = &ev
		parts := m.appendExecViewActionHints(nil)
		var joined string
		for _, p := range parts {
			joined += p + " "
		}
		if !strings.Contains(joined, "restart") {
			t.Fatalf("expected restart hint, got: %v", parts)
		}
	})

	t.Run("With launch ticket adds download hint", func(t *testing.T) {
		m := newTestModel(nil)
		m.launchTicketFunc = func() (string, error) { return "tkt", nil }
		ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1"})
		m.execView = &ev
		parts := m.appendExecViewActionHints(nil)
		var joined string
		for _, p := range parts {
			joined += p + " "
		}
		if !strings.Contains(joined, "download") {
			t.Fatalf("expected download hint, got: %v", parts)
		}
	})

	t.Run("With deletable run adds D hint", func(t *testing.T) {
		m := newTestModel(nil)
		success := model.ReasonSuccess
		ev := execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &success})
		m.execView = &ev
		parts := m.appendExecViewActionHints(nil)
		var joined string
		for _, p := range parts {
			joined += p + " "
		}
		if !strings.Contains(joined, "delete") {
			t.Fatalf("expected delete hint, got: %v", parts)
		}
	})

	t.Run("Nil Run returns parts unchanged", func(t *testing.T) {
		m := newTestModel(nil)
		ev := execlist.NewExecView(nil)
		m.execView = &ev
		parts := m.appendExecViewActionHints([]string{"original"})
		if len(parts) != 1 || parts[0] != "original" {
			t.Fatalf("expected unchanged parts, got: %v", parts)
		}
	})
}

// ─── buildMainHelpText additional branches ────────────────────────────────────

// TestBuildMainHelpText_PageDebug covers the PageDebug branch.
func TestBuildMainHelpText_PageDebug(t *testing.T) {
	m := newTestModel(nil)
	// Without tasks, items are [Home(0), Info(1), Debug(2)]
	selectSidebarItem(&m, 2) // Debug
	m.focusMainPanel()
	got := m.buildMainHelpText()
	if !strings.Contains(got, "scroll") {
		t.Fatalf("expected scroll hint on PageDebug, got: %q", got)
	}
}

// TestBuildMainHelpText_HomeCursorOnOpenWebUI covers the FieldOpenWebUI branch
// of buildMainHelpText with homeCursor>=0.
func TestBuildMainHelpText_HomeCursorOnOpenWebUI(t *testing.T) {
	m := newTestModel(nil)
	m.info.Port = 8181
	m.launchTicketFunc = func() (string, error) { return "tkt", nil }
	m.focusHomeField(0) // FieldOpenWebUI is index 0 when launch ticket present
	got := m.buildMainHelpText()
	if !strings.Contains(got, "open") {
		t.Fatalf("expected open hint for FieldOpenWebUI cursor, got: %q", got)
	}
}

// TestBuildMainHelpText_HomeWithNotifications adds the notifications hint.
func TestBuildMainHelpText_HomeWithNotifications(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	m.notifications.Upsert(testNotif("n1"))
	got := m.buildMainHelpText()
	if !strings.Contains(got, "n notifications") {
		t.Fatalf("expected notifications hint, got: %q", got)
	}
}
