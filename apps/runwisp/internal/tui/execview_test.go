// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/model"
)

func newTestRun() *model.Run {
	return &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "test-task",
		Status:      model.PhaseRunning,
		TriggeredBy: "manual",
		CreatedAt:   time.Now(),
	}
}

func newSizedExecView(w, h int) ExecView {
	ev := NewExecView(newTestRun())
	ev.SetSize(w, h)
	ev.SetFocused(true)
	return ev
}

func appendLines(ev *ExecView, count int) {
	for i := 0; i < count; i++ {
		ev.pane.AppendLine(int64(i), "stdout", "line")
	}
}

func TestExecView_AppendLine_SingleLine(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.pane.AppendLine(0, "stdout", "hello world")

	if len(ev.pane.lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(ev.pane.lines))
	}
	if ev.pane.lines[0].text != "hello world" {
		t.Fatalf("expected 'hello world', got %q", ev.pane.lines[0].text)
	}
}

func TestExecView_AppendLine_MultipleLines(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.pane.AppendLine(0, "stdout", "line1")
	ev.pane.AppendLine(1, "stdout", "line2")
	ev.pane.AppendLine(2, "stdout", "line3")

	if len(ev.pane.lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(ev.pane.lines))
	}
	if ev.pane.lines[2].text != "line3" {
		t.Fatalf("expected 'line3', got %q", ev.pane.lines[2].text)
	}
}

func TestExecView_AppendLine_StreamPropagated(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.pane.AppendLine(0, "stderr", "err line")
	if ev.pane.lines[0].stream != "stderr" {
		t.Fatalf("expected stderr stream, got %q", ev.pane.lines[0].stream)
	}
}

func TestExecView_Append_Eviction(t *testing.T) {
	ev := newSizedExecView(80, 24)

	count := maxLogLines + 10
	for i := 0; i < count; i++ {
		ev.pane.AppendLine(int64(i), "stdout", "line")
	}

	if len(ev.pane.lines) != maxLogLines {
		t.Fatalf("expected %d lines after eviction, got %d", maxLogLines, len(ev.pane.lines))
	}
	if ev.pane.totalLines < count {
		t.Fatalf("expected totalLines>=%d, got %d", count, ev.pane.totalLines)
	}
}

func TestExecView_FollowMode(t *testing.T) {
	ev := newSizedExecView(80, 24)
	if !ev.pane.follow {
		t.Fatal("expected follow=true initially")
	}

	appendLines(&ev, 50)
	if ev.pane.scroll != ev.pane.maxScroll() {
		t.Fatalf("expected scroll=%d (bottom), got %d", ev.pane.maxScroll(), ev.pane.scroll)
	}
}

func TestExecView_Update_ScrollUp(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 50)

	ev.Update(tea.KeyMsg{Type: tea.KeyUp})

	if ev.pane.follow {
		t.Fatal("expected follow=false after scrolling up")
	}
}

func TestExecView_Update_ScrollToBottom(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 50)
	ev.pane.scroll = 0
	ev.pane.follow = false

	ev.Update(tea.KeyMsg{Type: tea.KeyEnd})

	if !ev.pane.follow {
		t.Fatal("expected follow=true after G/end")
	}
	if ev.pane.scroll != ev.pane.maxScroll() {
		t.Fatalf("expected scroll=%d, got %d", ev.pane.maxScroll(), ev.pane.scroll)
	}
}

func TestExecView_Update_ScrollToTop(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 50)

	ev.Update(tea.KeyMsg{Type: tea.KeyHome})

	if ev.pane.scroll != 0 {
		t.Fatalf("expected scroll=0, got %d", ev.pane.scroll)
	}
	if ev.pane.hScroll != 0 {
		t.Fatalf("expected hScroll=0, got %d", ev.pane.hScroll)
	}
	if ev.pane.follow {
		t.Fatal("expected follow=false after g/home")
	}
}

func TestExecView_Update_HeaderFocusNavigation(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.run.Status = model.PhaseRunning

	// Scroll up from log area should enter header focus.
	ev.pane.scroll = 0
	ev.headerFocus = headerFocusNone
	ev.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ev.headerFocus != headerFocusStarted {
		t.Fatalf("expected headerFocusStarted, got %d", ev.headerFocus)
	}

	// Up again should go to ID row.
	ev.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ev.headerFocus != headerFocusID {
		t.Fatalf("expected headerFocusID, got %d", ev.headerFocus)
	}

	// Right from ID should go to Back, then to Action.
	ev.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if ev.headerFocus != headerFocusBack {
		t.Fatalf("expected headerFocusBack, got %d", ev.headerFocus)
	}

	ev.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ev.headerFocus != headerFocusID {
		t.Fatalf("expected headerFocusID after right from back, got %d", ev.headerFocus)
	}

	ev.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ev.headerFocus != headerFocusAction {
		t.Fatalf("expected headerFocusAction, got %d", ev.headerFocus)
	}
}

func TestExecView_Update_HorizontalScroll(t *testing.T) {
	ev := newSizedExecView(40, 24)
	ev.pane.AppendLine(0, "stdout", strings.Repeat("x", 200))
	ev.pane.scroll = 0
	ev.pane.follow = false

	ev.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ev.pane.hScroll != hScrollStep {
		t.Fatalf("expected hScroll=%d, got %d", hScrollStep, ev.pane.hScroll)
	}

	ev.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
	if ev.pane.hScroll != 0 {
		t.Fatalf("expected hScroll=0 after shift+left, got %d", ev.pane.hScroll)
	}
}

func TestExecView_Update_PageDown(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 100)
	ev.pane.scroll = 0
	ev.pane.follow = false

	visible := ev.pane.VisibleLines()
	ev.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if ev.pane.scroll != visible {
		t.Fatalf("expected scroll=%d after pgdown, got %d", visible, ev.pane.scroll)
	}
}

func TestExecView_CopyableValue(t *testing.T) {
	ev := newSizedExecView(80, 24)

	ev.headerFocus = headerFocusID
	val := ev.CopyableValue()
	if val != ev.run.ID {
		t.Fatalf("expected run ID, got %q", val)
	}

	ev.headerFocus = headerFocusNone
	val = ev.CopyableValue()
	if val != "" {
		t.Fatalf("expected empty for headerFocusNone, got %q", val)
	}
}

func TestExecView_RunID(t *testing.T) {
	run := newTestRun()
	ev := NewExecView(run)
	if ev.RunID() != run.ID {
		t.Fatalf("expected %s, got %s", run.ID, ev.RunID())
	}

	ev2 := NewExecView(nil)
	if ev2.RunID() != "" {
		t.Fatal("expected empty string for nil run")
	}
}

func TestExecView_VisibleLines(t *testing.T) {
	ev := newSizedExecView(80, 10)
	vis := ev.pane.VisibleLines()
	// height=10, header=4 → 6 visible
	if vis != 6 {
		t.Fatalf("expected 6 visible lines, got %d", vis)
	}

	ev.SetSize(80, 3)
	vis = ev.pane.VisibleLines()
	if vis != 1 {
		t.Fatalf("expected 1 visible line for tiny height, got %d", vis)
	}
}

func TestSliceLineColumns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		start    int
		cols     int
		expected string
		clipped  bool
	}{
		{"basic", "hello world", 0, 5, "hello", true},
		{"full", "hello", 0, 10, "hello", false},
		{"offset", "hello world", 6, 5, "world", false},
		{"beyond", "hi", 10, 5, "", false},
		{"tabs", "\thi", 0, 10, "    hi", false},
		{"zero cols", "hello", 0, 0, "", false},
		// ANSI escape sequences have zero visual width and must not disturb column counts.
		{"ansi color inline", "\x1b[32mhello\x1b[0m", 0, 5, "\x1b[32mhello\x1b[0m", false},
		{"ansi color clipped", "\x1b[32mhello world\x1b[0m", 0, 5, "\x1b[32mhello", true},
		// Color state set before the visible window must be propagated.
		{"ansi color before window", "\x1b[32mabcde\x1b[0mfoo", 5, 3, "\x1b[32m\x1b[0mfoo", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, clipped := sliceLineColumns(tt.input, tt.start, tt.cols)
			if got != tt.expected {
				t.Errorf("sliceLineColumns(%q, %d, %d) = %q, want %q", tt.input, tt.start, tt.cols, got, tt.expected)
			}
			if clipped != tt.clipped {
				t.Errorf("sliceLineColumns(%q, %d, %d) clipped = %v, want %v", tt.input, tt.start, tt.cols, clipped, tt.clipped)
			}
		})
	}
}

func TestExecView_HasActionButton(t *testing.T) {
	ev := newSizedExecView(80, 24)

	ev.run.Status = model.PhaseRunning
	if !ev.hasActionButton() {
		t.Fatal("expected action button for running status")
	}

	ev.run.Status = model.PhaseEnded
	ev.run.EndReason = model.EndReasonPtr(model.ReasonSuccess)
	if ev.hasActionButton() {
		t.Fatal("expected no action button for success status")
	}

	ev.run.Status = model.PhaseEnded
	ev.run.EndReason = model.EndReasonPtr(model.ReasonFailed)
	if !ev.hasActionButton() {
		t.Fatal("expected action button for failed status")
	}
}

func TestExecView_ToggleFullscreen(t *testing.T) {
	ev := newSizedExecView(80, 24)
	if ev.Fullscreen() {
		t.Fatal("expected fullscreen=false initially")
	}
	if !ev.pane.cfg.LineNumbers {
		t.Fatal("expected line numbers enabled initially")
	}
	// Normal mode: header takes 4 lines of the 24-high viewport.
	if got := ev.pane.VisibleLines(); got != 20 {
		t.Fatalf("expected 20 visible lines in normal mode, got %d", got)
	}

	ev.headerFocus = headerFocusID
	ev.hoveredHeader = headerFocusBack
	ev.ToggleFullscreen()

	if !ev.Fullscreen() {
		t.Fatal("expected fullscreen=true after toggle")
	}
	if ev.pane.cfg.LineNumbers {
		t.Fatal("expected line numbers disabled in fullscreen")
	}
	if ev.headerFocus != headerFocusNone {
		t.Fatalf("expected headerFocus cleared, got %d", ev.headerFocus)
	}
	if ev.hoveredHeader != headerFocusNone {
		t.Fatalf("expected hoveredHeader cleared, got %d", ev.hoveredHeader)
	}
	// Fullscreen: header height 0, so full 24 lines visible.
	if got := ev.pane.VisibleLines(); got != 24 {
		t.Fatalf("expected 24 visible lines in fullscreen, got %d", got)
	}
	if hit := ev.hitAt(5, 1); hit != headerFocusNone {
		t.Fatalf("expected no header hitbox in fullscreen, got %d", hit)
	}

	ev.ToggleFullscreen()
	if ev.Fullscreen() {
		t.Fatal("expected fullscreen=false after second toggle")
	}
	if !ev.pane.cfg.LineNumbers {
		t.Fatal("expected line numbers re-enabled after exiting fullscreen")
	}
	if got := ev.pane.VisibleLines(); got != 20 {
		t.Fatalf("expected 20 visible lines after exit, got %d", got)
	}
}

func TestExecView_Fullscreen_ViewHasNoHeader(t *testing.T) {
	ev := newSizedExecView(80, 10)
	ev.pane.AppendLine(0, "stdout", "hello world")
	ev.ToggleFullscreen()

	out := ev.View()
	// The normal-mode header renders "← Back" and the task name; neither should appear in fullscreen.
	if strings.Contains(out, "← Back") {
		t.Fatalf("fullscreen view leaked back button: %q", out)
	}
	if strings.Contains(out, "test-task") {
		t.Fatalf("fullscreen view leaked task name: %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Fatalf("fullscreen view missing log content: %q", out)
	}
}

func TestExecView_Fullscreen_UpDoesNotEnterHeader(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.ToggleFullscreen()
	ev.pane.scroll = 0

	ev.Update(tea.KeyMsg{Type: tea.KeyUp})

	if ev.headerFocus != headerFocusNone {
		t.Fatalf("fullscreen up must not enter header; got focus=%d", ev.headerFocus)
	}
}

func TestExecView_Fullscreen_StillScrollsWithKeys(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 100)
	ev.ToggleFullscreen()
	ev.pane.scroll = 0
	ev.pane.follow = false

	ev.Update(tea.KeyMsg{Type: tea.KeyPgDown})

	if ev.pane.scroll == 0 {
		t.Fatal("expected pgdown to advance scroll in fullscreen")
	}
}

func TestExecView_NotFocused_IgnoresInput(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.SetFocused(false)
	appendLines(&ev, 50)
	initialScroll := ev.pane.scroll

	ev.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ev.pane.scroll != initialScroll {
		t.Fatal("expected no scroll change when not focused")
	}
}
