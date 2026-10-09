// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/execlist"
)

func TestView_NotReadyShowsInitializing(t *testing.T) {
	m := newTestModel(nil)
	// Default ready is false until handleWindowSize fires.
	if got := m.View().Content; got != "Initializing..." {
		t.Fatalf("not-ready View() = %q", got)
	}
}

func TestView_ReadyRendersBodyAndHelpBar(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	got := m.View().Content
	if got == "" {
		t.Fatal("expected non-empty View output")
	}
	// Help bar text comes from buildHelpText — sidebar focus default includes
	// the navigation hint.
	if !strings.Contains(got, "navigate") {
		t.Fatalf("expected help-bar hint in View output, got: %q", got)
	}
}

func TestRenderHelpBar_NoFlashReturnsHelp(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	got := m.renderHelpBar()
	if !strings.Contains(got, "navigate") {
		t.Fatalf("expected base help text, got: %q", got)
	}
}

func TestRenderHelpBar_OmitsFlash(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.dialogs.Flash("Saved", 5*time.Second)
	if got := m.renderHelpBar(); strings.Contains(got, "Saved") {
		t.Fatalf("flash must render as a toast, not in the help bar: %q", got)
	}
}

// TestView_ShowsToastAboveHelpBar verifies the flash floats as a toast in the
// bottom-right corner while the help bar stays intact on the last line.
func TestView_ShowsToastAboveHelpBar(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.dialogs.FlashUndo("Deleted run", func() tea.Msg { return nil }, 5*time.Second)
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(lines) != 30 {
		t.Fatalf("expected 30 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[29], "navigate") {
		t.Fatalf("help bar must stay on the last line, got %q", lines[29])
	}
	above := strings.Join(lines[24:29], "\n")
	if !strings.Contains(above, "✓ Deleted run") || !strings.Contains(above, "u undo") {
		t.Fatalf("expected toast with undo hint above the help bar, got:\n%s", above)
	}
	for i, ln := range lines[:29] {
		if w := ansi.StringWidth(ln); w > 120 {
			t.Fatalf("line %d overflows terminal: width %d", i, w)
		}
	}
}

func TestRenderToast_ErrorUsesCross(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.dialogs.FlashError("Stop failed: boom", 5*time.Second)
	toast, ok := m.renderToast()
	if !ok || !strings.Contains(ansi.Strip(toast), "✗ Stop failed: boom") {
		t.Fatalf("expected error toast, got %q (ok=%v)", toast, ok)
	}
}

func TestRenderBody_DefaultSidebarPlusMain(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	// Default: no execView → renderBody joins sidebar and main horizontally.
	body := m.renderBody()
	if body == "" {
		t.Fatal("expected non-empty body")
	}
}

func TestRenderBody_FullscreenExecViewTrimsTrailingNewline(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
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
	m := newTestModel([]model.Task{{Name: "t1"}})
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	// Sidebar items: [Home(0), t1(1), Info(2), Debug(3)] — pick Info.
	selectSidebarItem(&m, 2)

	main := m.renderMainContent()
	if main == "" {
		t.Fatal("expected non-empty Info page content")
	}
}

func TestRenderMainContent_PageDebugUsesDebugView(t *testing.T) {
	m := newTestModel([]model.Task{{Name: "t1"}})
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
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
	got := m.buildHelpText().String()
	if !strings.Contains(got, "mark read") {
		t.Fatalf("expanded-notifications hint missing: %q", got)
	}
}

func TestBuildHelpText_ExecViewHintIncludesQuit(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	got := m.buildHelpText().String()
	if !strings.Contains(got, "q/^C quit") {
		t.Fatalf("expected quit hint in exec-view help, got: %q", got)
	}
}

func TestBuildSidebarHelpText_ServiceShowsRestart(t *testing.T) {
	m := newTestModel([]model.Task{{Name: "svc", Kind: model.KindService}})
	// Place the cursor on the service entry (item 1) without pressing Enter so
	// CursorTaskName returns the service.
	m.sidebar.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	got := m.buildSidebarHelpText().String()
	if !strings.Contains(got, "restart") {
		t.Fatalf("expected restart hint for service, got: %q", got)
	}
}

func TestBuildSidebarHelpText_NoCursorTaskOmitsActionHint(t *testing.T) {
	m := newTestModel(nil)
	got := m.buildSidebarHelpText().String()
	if strings.Contains(got, " r ") {
		t.Fatalf("expected no action hint when cursor isn't on a task, got: %q", got)
	}
}

// ─── renderHomeContent ───────────────────────────────────────────────────────

// TestRenderHomeContent_NoActiveTaskRendersHomeHeader covers the no-active-task
// branch which calls home.RenderHeader and includes the exec list view.
func TestRenderHomeContent_NoActiveTaskRendersHomeHeader(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	got := m.renderHomeContent(80, "")
	if got == "" {
		t.Fatal("expected non-empty home content with no active task")
	}
}

// TestRenderHomeContent_ActiveTaskRendersTaskHeader covers the active-task
// branch which calls home.RenderTaskHeader.
func TestRenderHomeContent_ActiveTaskRendersTaskHeader(t *testing.T) {
	tasks := []model.Task{{Name: "backup"}}
	m := newTestModel(tasks)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	selectSidebarItem(&m, 1)
	got := m.renderHomeContent(80, "")
	if got == "" {
		t.Fatal("expected non-empty content for active task")
	}
}

// TestRenderHomeContent_WithPanelView verifies the panel-view is prepended to
// the rendered content.
func TestRenderHomeContent_WithPanelView(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
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
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
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
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
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
	got := m.buildHelpText().String()
	if !strings.Contains(got, "sidebar") && !strings.Contains(got, "navigate") {
		t.Fatalf("expected sidebar help text, got: %q", got)
	}
}

// TestBuildHelpText_MainPanelRoutes covers the main-help-text branch.
func TestBuildHelpText_MainPanelRoutes(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	got := m.buildHelpText().String()
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
	got := m.buildExecViewHelpText().String()
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
	got := m.buildExecViewHelpText().String()
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
	got := m.buildExecViewHelpText().String()
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
	got := m.buildExecViewHelpText().String()
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
	got := m.buildExecViewHelpText().String()
	if !strings.Contains(got, "fullscreen") {
		t.Fatalf("expected fullscreen hint for default scroll branch, got: %q", got)
	}
}

// ─── appendExecViewActionHints ────────────────────────────────────────────────

// execViewHints returns the action hints appendExecViewActionHints adds for a
// model showing ev.
func execViewHints(m Model, ev execlist.ExecView) string {
	m.execView = &ev
	return m.appendExecViewActionHints(nil).String()
}

// TestAppendExecViewActionHints_AllActionsAndExtras covers the four Action
// branches plus the launch-ticket and CanDelete extras.
func TestAppendExecViewActionHints_AllActionsAndExtras(t *testing.T) {
	failed, success := model.ReasonFailed, model.ReasonSuccess
	service := func(r *model.Run) execlist.ExecView {
		ev := execlist.NewExecView(r)
		ev.TaskIsService = true
		return ev
	}
	stoppedService := service(&model.Run{ID: "r1", TaskName: "t1"})
	stoppedService.SetServiceStopped(true)
	withTicket := newTestModel(nil)
	withTicket.launchTicketFunc = func() (string, error) { return "tkt", nil }

	cases := []struct {
		name string
		m    Model
		ev   execlist.ExecView
		want string
	}{
		{"ActionStop", newTestModel(nil), execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}), "stop"},
		{"ActionStopService", newTestModel(nil), service(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseRunning}), "stop service"},
		{"ActionRetry", newTestModel(nil), execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &failed}), "retry"},
		{"ActionRestartService", newTestModel(nil), stoppedService, "restart"},
		{"With launch ticket adds download hint", withTicket, execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1"}), "download"},
		{"With deletable run adds D hint", newTestModel(nil), execlist.NewExecView(&model.Run{ID: "r1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &success}), "delete"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := execViewHints(c.m, c.ev); !strings.Contains(got, c.want) {
				t.Fatalf("expected %q hint, got: %q", c.want, got)
			}
		})
	}

	t.Run("Nil Run returns parts unchanged", func(t *testing.T) {
		m := newTestModel(nil)
		ev := execlist.NewExecView(nil)
		m.execView = &ev
		parts := m.appendExecViewActionHints(helpBar{{"original", prioNav}})
		if len(parts) != 1 || parts[0].text != "original" {
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
	got := m.buildMainHelpText().String()
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
	got := m.buildMainHelpText().String()
	if !strings.Contains(got, "open") {
		t.Fatalf("expected open hint for FieldOpenWebUI cursor, got: %q", got)
	}
}

// TestBuildMainHelpText_HomeWithNotifications adds the notifications hint.
func TestBuildMainHelpText_HomeWithNotifications(t *testing.T) {
	m := newTestModel(nil)
	m.focusMainPanel()
	m.notifications.Upsert(testNotif("n1"))
	got := m.buildMainHelpText().String()
	if !strings.Contains(got, "n notifications") {
		t.Fatalf("expected notifications hint, got: %q", got)
	}
}
