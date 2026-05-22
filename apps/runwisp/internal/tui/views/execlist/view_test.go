// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package execlist

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/server/dto"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/logpane"
)

func newTestRun() *dto.Run {
	return &dto.Run{
		ID:          ulid.Make().String(),
		TaskName:    "test-task",
		Status:      sqlcdb.PhaseRunning,
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
		ev.Pane.AppendLine(int64(i), "stdout", "line")
	}
}

func TestExecView_AppendLine_SingleLine(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Pane.AppendLine(0, "stdout", "hello world")

	if len(ev.Pane.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(ev.Pane.Lines))
	}
	if ev.Pane.Lines[0].Text != "hello world" {
		t.Fatalf("expected 'hello world', got %q", ev.Pane.Lines[0].Text)
	}
}

func TestExecView_AppendLine_MultipleLines(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Pane.AppendLine(0, "stdout", "line1")
	ev.Pane.AppendLine(1, "stdout", "line2")
	ev.Pane.AppendLine(2, "stdout", "line3")

	if len(ev.Pane.Lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(ev.Pane.Lines))
	}
	if ev.Pane.Lines[2].Text != "line3" {
		t.Fatalf("expected 'line3', got %q", ev.Pane.Lines[2].Text)
	}
}

func TestExecView_AppendLine_StreamPropagated(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Pane.AppendLine(0, "stderr", "err line")
	if ev.Pane.Lines[0].Stream != "stderr" {
		t.Fatalf("expected stderr stream, got %q", ev.Pane.Lines[0].Stream)
	}
}

func TestExecView_Append_Eviction(t *testing.T) {
	ev := newSizedExecView(80, 24)

	count := MaxLogLines + 10
	for i := 0; i < count; i++ {
		ev.Pane.AppendLine(int64(i), "stdout", "line")
	}

	if len(ev.Pane.Lines) != MaxLogLines {
		t.Fatalf("expected %d lines after eviction, got %d", MaxLogLines, len(ev.Pane.Lines))
	}
	if ev.Pane.TotalLines < count {
		t.Fatalf("expected totalLines>=%d, got %d", count, ev.Pane.TotalLines)
	}
}

func TestExecView_FollowMode(t *testing.T) {
	ev := newSizedExecView(80, 24)
	if !ev.Pane.Follow {
		t.Fatal("expected follow=true initially")
	}

	appendLines(&ev, 50)
	if ev.Pane.Scroll != ev.Pane.MaxScroll() {
		t.Fatalf("expected scroll=%d (bottom), got %d", ev.Pane.MaxScroll(), ev.Pane.Scroll)
	}
}

func TestExecView_Update_ScrollUp(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 50)

	ev.Update(tea.KeyMsg{Type: tea.KeyUp})

	if ev.Pane.Follow {
		t.Fatal("expected follow=false after scrolling up")
	}
}

func TestExecView_Update_ScrollToBottom(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 50)
	ev.Pane.Scroll = 0
	ev.Pane.Follow = false

	ev.Update(tea.KeyMsg{Type: tea.KeyEnd})

	if !ev.Pane.Follow {
		t.Fatal("expected follow=true after G/end")
	}
	if ev.Pane.Scroll != ev.Pane.MaxScroll() {
		t.Fatalf("expected scroll=%d, got %d", ev.Pane.MaxScroll(), ev.Pane.Scroll)
	}
}

func TestExecView_Update_ScrollToTop(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 50)

	ev.Update(tea.KeyMsg{Type: tea.KeyHome})

	if ev.Pane.Scroll != 0 {
		t.Fatalf("expected scroll=0, got %d", ev.Pane.Scroll)
	}
	if ev.Pane.HScroll != 0 {
		t.Fatalf("expected hScroll=0, got %d", ev.Pane.HScroll)
	}
	if ev.Pane.Follow {
		t.Fatal("expected follow=false after g/home")
	}
}

func TestExecView_Update_HeaderFocusNavigation(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Run.Status = sqlcdb.PhaseRunning

	// Scroll up from log area should enter header focus.
	ev.Pane.Scroll = 0
	ev.HeaderFocus = HeaderFocusNone
	ev.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ev.HeaderFocus != HeaderFocusStarted {
		t.Fatalf("expected HeaderFocusStarted, got %d", ev.HeaderFocus)
	}

	// Up again should go to ID row.
	ev.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ev.HeaderFocus != HeaderFocusID {
		t.Fatalf("expected HeaderFocusID, got %d", ev.HeaderFocus)
	}

	// Right from ID should go to Back, then to Action.
	ev.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if ev.HeaderFocus != HeaderFocusBack {
		t.Fatalf("expected HeaderFocusBack, got %d", ev.HeaderFocus)
	}

	ev.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ev.HeaderFocus != HeaderFocusID {
		t.Fatalf("expected HeaderFocusID after right from back, got %d", ev.HeaderFocus)
	}

	ev.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ev.HeaderFocus != HeaderFocusAction {
		t.Fatalf("expected HeaderFocusAction, got %d", ev.HeaderFocus)
	}
}

func TestExecView_Update_HorizontalScroll(t *testing.T) {
	ev := newSizedExecView(40, 24)
	ev.Pane.AppendLine(0, "stdout", strings.Repeat("x", 200))
	ev.Pane.Scroll = 0
	ev.Pane.Follow = false

	ev.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ev.Pane.HScroll != logpane.HScrollStep {
		t.Fatalf("expected hScroll=%d, got %d", logpane.HScrollStep, ev.Pane.HScroll)
	}

	ev.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
	if ev.Pane.HScroll != 0 {
		t.Fatalf("expected hScroll=0 after shift+left, got %d", ev.Pane.HScroll)
	}
}

func TestExecView_Update_PageDown(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 100)
	ev.Pane.Scroll = 0
	ev.Pane.Follow = false

	visible := ev.Pane.VisibleLines()
	ev.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if ev.Pane.Scroll != visible {
		t.Fatalf("expected scroll=%d after pgdown, got %d", visible, ev.Pane.Scroll)
	}
}

func TestExecView_CopyableValue(t *testing.T) {
	ev := newSizedExecView(80, 24)

	ev.HeaderFocus = HeaderFocusID
	val := ev.CopyableValue()
	if val != ev.Run.ID {
		t.Fatalf("expected run ID, got %q", val)
	}

	ev.HeaderFocus = HeaderFocusNone
	val = ev.CopyableValue()
	if val != "" {
		t.Fatalf("expected empty for HeaderFocusNone, got %q", val)
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
	vis := ev.Pane.VisibleLines()
	// height=10, header=4 → 6 visible
	if vis != 6 {
		t.Fatalf("expected 6 visible lines, got %d", vis)
	}

	ev.SetSize(80, 3)
	vis = ev.Pane.VisibleLines()
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
			got, clipped := uikit.SliceLineColumns(tt.input, tt.start, tt.cols)
			if got != tt.expected {
				t.Errorf("uikit.SliceLineColumns(%q, %d, %d) = %q, want %q", tt.input, tt.start, tt.cols, got, tt.expected)
			}
			if clipped != tt.clipped {
				t.Errorf("uikit.SliceLineColumns(%q, %d, %d) clipped = %v, want %v", tt.input, tt.start, tt.cols, clipped, tt.clipped)
			}
		})
	}
}

func TestExecView_HasActionButton(t *testing.T) {
	ev := newSizedExecView(80, 24)

	ev.Run.Status = sqlcdb.PhaseRunning
	if !ev.hasActionButton() {
		t.Fatal("expected action button for running status")
	}

	ev.Run.Status = sqlcdb.PhaseEnded
	ev.Run.EndReason = sqlcdb.EndReasonPtr(sqlcdb.ReasonSuccess)
	if !ev.hasActionButton() {
		t.Fatal("expected action button (Delete) for success status")
	}

	ev.Run.Status = sqlcdb.PhaseEnded
	ev.Run.EndReason = sqlcdb.EndReasonPtr(sqlcdb.ReasonFailed)
	if !ev.hasActionButton() {
		t.Fatal("expected action button for failed status")
	}

	ev.Run.Status = sqlcdb.PhasePending
	ev.Run.EndReason = nil
	if ev.hasActionButton() {
		t.Fatal("expected no action button for pending status")
	}
}

func TestExecView_ToggleFullscreen(t *testing.T) {
	ev := newSizedExecView(80, 24)
	if ev.Fullscreen() {
		t.Fatal("expected fullscreen=false initially")
	}
	if !ev.Pane.Cfg.LineNumbers {
		t.Fatal("expected line numbers enabled initially")
	}
	// Normal mode: header takes 4 lines of the 24-high viewport.
	if got := ev.Pane.VisibleLines(); got != 20 {
		t.Fatalf("expected 20 visible lines in normal mode, got %d", got)
	}

	ev.HeaderFocus = HeaderFocusID
	ev.HoveredHeader = HeaderFocusBack
	ev.ToggleFullscreen()

	if !ev.Fullscreen() {
		t.Fatal("expected fullscreen=true after toggle")
	}
	if ev.Pane.Cfg.LineNumbers {
		t.Fatal("expected line numbers disabled in fullscreen")
	}
	if ev.HeaderFocus != HeaderFocusNone {
		t.Fatalf("expected headerFocus cleared, got %d", ev.HeaderFocus)
	}
	if ev.HoveredHeader != HeaderFocusNone {
		t.Fatalf("expected hoveredHeader cleared, got %d", ev.HoveredHeader)
	}
	// Fullscreen: header height 0, so full 24 lines visible.
	if got := ev.Pane.VisibleLines(); got != 24 {
		t.Fatalf("expected 24 visible lines in fullscreen, got %d", got)
	}
	if hit := ev.HitAt(5, 1); hit != HeaderFocusNone {
		t.Fatalf("expected no header hitbox in fullscreen, got %d", hit)
	}

	ev.ToggleFullscreen()
	if ev.Fullscreen() {
		t.Fatal("expected fullscreen=false after second toggle")
	}
	if !ev.Pane.Cfg.LineNumbers {
		t.Fatal("expected line numbers re-enabled after exiting fullscreen")
	}
	if got := ev.Pane.VisibleLines(); got != 20 {
		t.Fatalf("expected 20 visible lines after exit, got %d", got)
	}
}

func TestExecView_Fullscreen_ViewHasNoHeader(t *testing.T) {
	ev := newSizedExecView(80, 10)
	ev.Pane.AppendLine(0, "stdout", "hello world")
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
	ev.Pane.Scroll = 0

	ev.Update(tea.KeyMsg{Type: tea.KeyUp})

	if ev.HeaderFocus != HeaderFocusNone {
		t.Fatalf("fullscreen up must not enter header; got focus=%d", ev.HeaderFocus)
	}
}

func TestExecView_Fullscreen_StillScrollsWithKeys(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 100)
	ev.ToggleFullscreen()
	ev.Pane.Scroll = 0
	ev.Pane.Follow = false

	ev.Update(tea.KeyMsg{Type: tea.KeyPgDown})

	if ev.Pane.Scroll == 0 {
		t.Fatal("expected pgdown to advance scroll in fullscreen")
	}
}

func TestExecView_NotFocused_IgnoresInput(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.SetFocused(false)
	appendLines(&ev, 50)
	initialScroll := ev.Pane.Scroll

	ev.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ev.Pane.Scroll != initialScroll {
		t.Fatal("expected no scroll change when not focused")
	}
}

func TestExecView_HandleKeyLeft_AllTransitions(t *testing.T) {
	tests := []struct {
		name    string
		initial HeaderFocusItem
		want    HeaderFocusItem
	}{
		{"Action→ID", HeaderFocusAction, HeaderFocusID},
		{"ID→Back", HeaderFocusID, HeaderFocusBack},
		{"Duration→Started", HeaderFocusDuration, HeaderFocusStarted},
		{"Back→Back (no change)", HeaderFocusBack, HeaderFocusBack},
		{"Started→Started (no change)", HeaderFocusStarted, HeaderFocusStarted},
		// default: pane scroll handled
		{"None→None (pane handles)", HeaderFocusNone, HeaderFocusNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := newSizedExecView(80, 24)
			ev.HeaderFocus = tc.initial
			ev.Update(tea.KeyMsg{Type: tea.KeyLeft})
			if ev.HeaderFocus != tc.want {
				t.Fatalf("after left from %d: expected focus=%d, got %d", tc.initial, tc.want, ev.HeaderFocus)
			}
		})
	}
}

func TestExecView_HandleKeyRight_AllTransitions(t *testing.T) {
	tests := []struct {
		name    string
		initial HeaderFocusItem
		want    HeaderFocusItem
		// hasAction controls whether the run is in a state that has an action button
		running bool
	}{
		{"Back→ID", HeaderFocusBack, HeaderFocusID, false},
		{"ID→Action (running)", HeaderFocusID, HeaderFocusAction, true},
		{"Started→Duration", HeaderFocusStarted, HeaderFocusDuration, false},
		{"Action→Action (no change)", HeaderFocusAction, HeaderFocusAction, true},
		{"Duration→Duration (no change)", HeaderFocusDuration, HeaderFocusDuration, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := newSizedExecView(80, 24)
			if tc.running {
				ev.Run.Status = sqlcdb.PhaseRunning
			} else {
				ev.Run.Status = sqlcdb.PhaseEnded
				ev.Run.EndReason = sqlcdb.EndReasonPtr(sqlcdb.ReasonSuccess)
			}
			ev.HeaderFocus = tc.initial
			ev.Update(tea.KeyMsg{Type: tea.KeyRight})
			if ev.HeaderFocus != tc.want {
				t.Fatalf("after right from %d: expected focus=%d, got %d", tc.initial, tc.want, ev.HeaderFocus)
			}
		})
	}
}

func TestExecView_HandleKeyRight_IDNoAction(t *testing.T) {
	// When at ID and there is no action, focus stays at ID.
	ev := newSizedExecView(80, 24)
	ev.Run.Status = sqlcdb.PhasePending
	ev.Run.EndReason = nil
	ev.HeaderFocus = HeaderFocusID
	ev.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ev.HeaderFocus != HeaderFocusID {
		t.Fatalf("expected focus to stay at ID when no action button, got %d", ev.HeaderFocus)
	}
}

func TestExecView_HandleKeyDown_AllTransitions(t *testing.T) {
	tests := []struct {
		name    string
		initial HeaderFocusItem
		want    HeaderFocusItem
	}{
		{"Back→Started", HeaderFocusBack, HeaderFocusStarted},
		{"ID→Started", HeaderFocusID, HeaderFocusStarted},
		{"Action→Started", HeaderFocusAction, HeaderFocusStarted},
		{"Started→None", HeaderFocusStarted, HeaderFocusNone},
		{"Duration→None", HeaderFocusDuration, HeaderFocusNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := newSizedExecView(80, 24)
			ev.HeaderFocus = tc.initial
			ev.Update(tea.KeyMsg{Type: tea.KeyDown})
			if ev.HeaderFocus != tc.want {
				t.Fatalf("after down from %d: expected focus=%d, got %d", tc.initial, tc.want, ev.HeaderFocus)
			}
		})
	}
}

func TestExecView_HandleKeyUp_StartedAndDuration(t *testing.T) {
	for _, initial := range []HeaderFocusItem{HeaderFocusStarted, HeaderFocusDuration} {
		ev := newSizedExecView(80, 24)
		ev.HeaderFocus = initial
		ev.Update(tea.KeyMsg{Type: tea.KeyUp})
		if ev.HeaderFocus != HeaderFocusID {
			t.Fatalf("up from %d: expected HeaderFocusID, got %d", initial, ev.HeaderFocus)
		}
	}
}

func TestExecView_HandleKeyUp_AtTopFocusedItems(t *testing.T) {
	for _, initial := range []HeaderFocusItem{HeaderFocusBack, HeaderFocusID, HeaderFocusAction} {
		ev := newSizedExecView(80, 24)
		ev.HeaderFocus = initial
		ev.Update(tea.KeyMsg{Type: tea.KeyUp})
		// These don't change on up
		if ev.HeaderFocus != initial {
			t.Fatalf("up from %d: expected no change, got %d", initial, ev.HeaderFocus)
		}
	}
}

func TestExecView_HandleKeyUp_ScrollAtZero_EntersHeader(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Pane.Scroll = 0
	ev.HeaderFocus = HeaderFocusNone
	ev.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ev.HeaderFocus != HeaderFocusStarted {
		t.Fatalf("expected HeaderFocusStarted when scrolling up from top, got %d", ev.HeaderFocus)
	}
}

func TestExecView_HandleKeyUp_ScrollAboveZero_Scrolls(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 50)
	ev.Pane.Scroll = 10
	ev.Pane.Follow = false
	ev.HeaderFocus = HeaderFocusNone
	ev.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ev.Pane.Scroll != 9 {
		t.Fatalf("expected scroll=9 after up, got %d", ev.Pane.Scroll)
	}
	if ev.HeaderFocus != HeaderFocusNone {
		t.Fatalf("expected no header focus change, got %d", ev.HeaderFocus)
	}
}

func TestExecView_Action_ServiceStopped(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.TaskIsService = true
	ev.SetServiceStopped(true)
	if ev.Action() != ActionRestartService {
		t.Fatalf("expected ActionRestartService when service is stopped, got %d", ev.Action())
	}
}

func TestExecView_Action_ServiceRunning(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.TaskIsService = true
	ev.SetServiceStopped(false)
	if ev.Action() != ActionStopService {
		t.Fatalf("expected ActionStopService when service is running, got %d", ev.Action())
	}
}

func TestExecView_Action_NilRun(t *testing.T) {
	ev := NewExecView(nil)
	if ev.Action() != ActionNone {
		t.Fatalf("expected ActionNone for nil run, got %d", ev.Action())
	}
}

func TestExecView_Action_Running(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Run.Status = sqlcdb.PhaseRunning
	if ev.Action() != ActionStop {
		t.Fatalf("expected ActionStop for running task, got %d", ev.Action())
	}
}

func TestExecView_ServiceStopped_RoundTrip(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.SetServiceStopped(true)
	if !ev.ServiceStopped() {
		t.Fatal("expected ServiceStopped()=true after SetServiceStopped(true)")
	}
	ev.SetServiceStopped(false)
	if ev.ServiceStopped() {
		t.Fatal("expected ServiceStopped()=false after SetServiceStopped(false)")
	}
}

func TestExecView_CopyValueFor_Duration(t *testing.T) {
	ev := newSizedExecView(80, 24)
	val := ev.CopyValueFor(HeaderFocusDuration)
	// Just verify it's non-empty and doesn't panic.
	if val == "" {
		t.Fatal("expected non-empty duration copy value")
	}
}

func TestExecView_CopyValueFor_Started(t *testing.T) {
	ev := newSizedExecView(80, 24)
	val := ev.CopyValueFor(HeaderFocusStarted)
	if val == "" {
		t.Fatal("expected non-empty started copy value")
	}
}

func TestExecView_CopyValueFor_NilRun(t *testing.T) {
	ev := NewExecView(nil)
	for _, f := range []HeaderFocusItem{HeaderFocusStarted, HeaderFocusDuration, HeaderFocusID, HeaderFocusNone} {
		if val := ev.CopyValueFor(f); val != "" {
			t.Fatalf("expected empty value for nil run, focus=%d, got %q", f, val)
		}
	}
}

func TestExecView_MaxHScroll(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Pane.AppendLine(0, "stdout", strings.Repeat("x", 200))
	ev.Pane.Scroll = 0
	ev.Pane.Follow = false
	// MaxHScroll should be > 0 given a line wider than the viewport.
	mhs := ev.MaxHScroll()
	if mhs <= 0 {
		t.Fatalf("expected MaxHScroll > 0 for wide line, got %d", mhs)
	}
}

func TestExecView_HitAt_Normal(t *testing.T) {
	ev := newSizedExecView(80, 24)
	// In normal mode, hitAt delegates to headerLayout (no hitboxes added yet → returns None)
	hit := ev.HitAt(10, 2)
	if hit != HeaderFocusNone {
		t.Fatalf("expected HeaderFocusNone for empty layout, got %d", hit)
	}
}

func TestExecView_Update_NonKeyMsg_NoChange(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 20)
	scroll := ev.Pane.Scroll
	// A non-key message should be ignored.
	ev.Update("not a key message")
	if ev.Pane.Scroll != scroll {
		t.Fatal("non-key message must not change scroll")
	}
}

// ---- execHeaderLayout helpers ----

func TestHeaderLayout_AddAndHitAt(t *testing.T) {
	var l execHeaderLayout
	l.add(HeaderFocusBack, 10, 20, 1)
	l.add(HeaderFocusID, 25, 40, 1)

	// Hit inside Back box.
	if got := l.hitAt(15, 1); got != HeaderFocusBack {
		t.Fatalf("expected HeaderFocusBack, got %d", got)
	}
	// Hit inside ID box.
	if got := l.hitAt(30, 1); got != HeaderFocusID {
		t.Fatalf("expected HeaderFocusID, got %d", got)
	}
	// Miss: wrong y.
	if got := l.hitAt(15, 2); got != HeaderFocusNone {
		t.Fatalf("expected HeaderFocusNone for wrong y, got %d", got)
	}
	// Miss: outside all boxes.
	if got := l.hitAt(0, 1); got != HeaderFocusNone {
		t.Fatalf("expected HeaderFocusNone for x outside boxes, got %d", got)
	}
	// x == x1 is exclusive.
	if got := l.hitAt(20, 1); got != HeaderFocusNone {
		t.Fatalf("expected HeaderFocusNone for x==x1 (exclusive), got %d", got)
	}
}

func TestHeaderLayout_Reset(t *testing.T) {
	var l execHeaderLayout
	l.add(HeaderFocusBack, 0, 10, 0)
	l.reset()
	if got := l.hitAt(5, 0); got != HeaderFocusNone {
		t.Fatalf("expected no hits after reset, got %d", got)
	}
}

// ---- view_render.go methods ----

func TestRenderMetaField_NormalAndFocused(t *testing.T) {
	ev := newSizedExecView(80, 24)

	// Not focused, not hovered.
	out := ev.renderMetaField("Started", "2024-01-01 00:00:00", HeaderFocusStarted)
	if out == "" {
		t.Fatal("expected non-empty renderMetaField output")
	}

	// Focused.
	ev.HeaderFocus = HeaderFocusStarted
	out2 := ev.renderMetaField("Started", "2024-01-01 00:00:00", HeaderFocusStarted)
	if out2 == "" {
		t.Fatal("expected non-empty renderMetaField when focused")
	}

	// Hovered.
	ev.HeaderFocus = HeaderFocusNone
	ev.HoveredHeader = HeaderFocusStarted
	out3 := ev.renderMetaField("Started", "2024-01-01 00:00:00", HeaderFocusStarted)
	if out3 == "" {
		t.Fatal("expected non-empty renderMetaField when hovered")
	}
}

func TestRenderBackButton_NormalAndHovered(t *testing.T) {
	ev := newSizedExecView(80, 24)

	out := ev.renderBackButton()
	if !strings.Contains(out, "Back") {
		t.Fatalf("expected '← Back' in output, got %q", out)
	}

	ev.HoveredHeader = HeaderFocusBack
	out2 := ev.renderBackButton()
	if !strings.Contains(out2, "Back") {
		t.Fatalf("expected hovered back button to still contain 'Back', got %q", out2)
	}

	ev.HoveredHeader = HeaderFocusNone
	ev.HeaderFocus = HeaderFocusBack
	out3 := ev.renderBackButton()
	if !strings.Contains(out3, "Back") {
		t.Fatalf("expected focused back button to still contain 'Back', got %q", out3)
	}
}

func TestRenderActionButtons_AllActions(t *testing.T) {
	ev := newSizedExecView(80, 24)

	// ActionStop
	ev.Run.Status = sqlcdb.PhaseRunning
	out := ev.renderActionButtons()
	if !strings.Contains(out, "Stop") {
		t.Fatalf("expected Stop button, got %q", out)
	}

	// ActionRetry (failed run, non-service)
	ev.Run.Status = sqlcdb.PhaseEnded
	ev.Run.EndReason = sqlcdb.EndReasonPtr(sqlcdb.ReasonFailed)
	out2 := ev.renderActionButtons()
	if !strings.Contains(out2, "Retry") {
		t.Fatalf("expected Retry button, got %q", out2)
	}

	// ActionStopService
	ev.TaskIsService = true
	ev.SetServiceStopped(false)
	out3 := ev.renderActionButtons()
	if !strings.Contains(out3, "Stop") {
		t.Fatalf("expected Stop button for service, got %q", out3)
	}

	// ActionRestartService
	ev.SetServiceStopped(true)
	out4 := ev.renderActionButtons()
	if !strings.Contains(out4, "Restart") {
		t.Fatalf("expected Restart button for stopped service, got %q", out4)
	}

	// ActionDelete — success run, non-service (still deletable)
	ev.TaskIsService = false
	ev.Run.Status = sqlcdb.PhaseEnded
	ev.Run.EndReason = sqlcdb.EndReasonPtr(sqlcdb.ReasonSuccess)
	out5 := ev.renderActionButtons()
	if !strings.Contains(out5, "Delete") {
		t.Fatalf("expected Delete button for ended successful run, got %q", out5)
	}

	// ActionNone — pending run
	ev.Run.Status = sqlcdb.PhasePending
	ev.Run.EndReason = nil
	out6 := ev.renderActionButtons()
	if out6 != "" {
		t.Fatalf("expected empty output for pending run, got %q", out6)
	}
}

func TestRenderActionButtons_HoveredAndFocused(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Run.Status = sqlcdb.PhaseRunning

	// Hovered action
	ev.HoveredHeader = HeaderFocusAction
	out := ev.renderActionButtons()
	if !strings.Contains(out, "Stop") {
		t.Fatalf("expected Stop in hovered action, got %q", out)
	}

	// Focused action
	ev.HoveredHeader = HeaderFocusNone
	ev.HeaderFocus = HeaderFocusAction
	out2 := ev.renderActionButtons()
	if !strings.Contains(out2, "Stop") {
		t.Fatalf("expected Stop in focused action, got %q", out2)
	}
}

func TestExecView_View_Normal(t *testing.T) {
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 10)

	out := ev.View()
	if !strings.Contains(out, "← Back") {
		t.Fatalf("expected back button in normal view, got %q", out)
	}
	if !strings.Contains(out, "test-task") {
		t.Fatalf("expected task name in normal view")
	}
}

func TestExecView_View_NormalWithAction(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Run.Status = sqlcdb.PhaseRunning
	appendLines(&ev, 5)

	out := ev.View()
	if !strings.Contains(out, "Stop") {
		t.Fatalf("expected Stop button in view with running run")
	}
}

func TestExecView_View_FollowIndicator(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Run.Status = sqlcdb.PhaseRunning
	ev.Pane.Follow = true
	appendLines(&ev, 5)

	out := ev.View()
	if !strings.Contains(out, "FOLLOW") {
		t.Fatalf("expected FOLLOW indicator, got view without it")
	}
}

func TestExecView_View_LoadingOlder(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.LoadingOlder = true
	ev.Pane.Scroll = 0
	appendLines(&ev, 5)

	out := ev.View()
	if !strings.Contains(out, "Loading older") {
		t.Fatalf("expected 'Loading older' in output, got: %q", out)
	}
}

func TestExecView_HandleKeyDown_DefaultBranch(t *testing.T) {
	// When focus is HeaderFocusNone, down should call Pane.HandleKeyScroll("down")
	// and not change HeaderFocus.
	ev := newSizedExecView(80, 24)
	appendLines(&ev, 100)
	ev.Pane.Scroll = 0
	ev.Pane.Follow = false
	ev.HeaderFocus = HeaderFocusNone

	ev.Update(tea.KeyMsg{Type: tea.KeyDown})
	// Scroll should have advanced since pane handled the key.
	if ev.Pane.Scroll == 0 {
		t.Fatal("expected scroll to advance when HeaderFocusNone and key=down")
	}
}

func TestExecView_SetSize_Fullscreen(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.ToggleFullscreen()
	// In fullscreen, SetSize should set HeaderHeight to 0.
	ev.SetSize(100, 30)
	if ev.Pane.HeaderH != 0 {
		t.Fatalf("expected HeaderH=0 in fullscreen after SetSize, got %d", ev.Pane.HeaderH)
	}
}

func TestExecView_View_InstanceIndex(t *testing.T) {
	ev := newSizedExecView(80, 24)
	ev.Run.InstanceIndex = 3
	appendLines(&ev, 5)

	out := ev.View()
	if !strings.Contains(out, "#3") {
		t.Fatalf("expected instance index in view output")
	}
}
