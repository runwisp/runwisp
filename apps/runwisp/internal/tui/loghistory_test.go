// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/stretchr/testify/assert"
)

var assertAnError = errors.New("boom")

// mustModel narrows a (tea.Model, tea.Cmd) pair back to the concrete Model so
// tests can inspect its fields.
func mustModel(tm tea.Model, cmd tea.Cmd) (Model, tea.Cmd) {
	m, ok := tm.(Model)
	if !ok {
		panic("expected tui.Model")
	}
	return m, cmd
}

func TestLogHistoryDialog_ViewShowsFramesWholeAndCommitted(t *testing.T) {
	frames := [][]string{
		{"Pulling [=>        ] 10%"},
		{"Pulling [=====>    ] 55%"},
	}
	d := NewLogHistoryDialog(7, frames, "Pulling [=========] 100%")

	out := d.View(100, 40)

	// Anchor line number is shown 1-based.
	assert.Contains(t, out, "line 8")
	// Each captured frame is labelled and rendered whole.
	assert.Contains(t, out, "Frame 1 of 2")
	assert.Contains(t, out, "Frame 2 of 2")
	assert.Contains(t, out, "10%")
	assert.Contains(t, out, "55%")
	// The settled line is labelled and shown last.
	assert.Contains(t, out, "committed")
	assert.Contains(t, out, "100%")
}

func TestLogHistoryDialog_MultiRowFramesShownWhole(t *testing.T) {
	frames := [][]string{
		{"layer-a downloading", "layer-b waiting"},
		{"layer-a extracting", "layer-b downloading"},
	}
	d := NewLogHistoryDialog(0, frames, "layer-a done\nlayer-b done")

	out := d.View(100, 40)
	for _, want := range []string{"layer-a downloading", "layer-b waiting", "layer-a extracting", "layer-b downloading"} {
		assert.Contains(t, out, want)
	}
}

func TestLogHistoryDialog_UpdateClosesOnKeys(t *testing.T) {
	for _, k := range []string{"esc", "enter", "q"} {
		t.Run(k, func(t *testing.T) {
			d := NewLogHistoryDialog(0, [][]string{{"x"}}, "y")
			var msg tea.KeyMsg
			switch k {
			case "esc":
				msg = tea.KeyMsg{Type: tea.KeyEsc}
			case "enter":
				msg = tea.KeyMsg{Type: tea.KeyEnter}
			case "q":
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
			}
			assert.True(t, d.Update(msg))
		})
	}
}

func TestLogHistoryDialog_ScrollClampsAndDoesNotClose(t *testing.T) {
	// Build enough frames to exceed the visible window.
	var frames [][]string
	for i := 0; i < 30; i++ {
		frames = append(frames, []string{"row"})
	}
	d := NewLogHistoryDialog(0, frames, "final")

	// Up at the top is a no-op (stays at 0) and never closes.
	assert.False(t, d.Update(tea.KeyMsg{Type: tea.KeyUp}))
	assert.Equal(t, 0, d.scroll)

	// Down advances; End jumps to the bottom; Down at the bottom clamps.
	assert.False(t, d.Update(tea.KeyMsg{Type: tea.KeyDown}))
	assert.Equal(t, 1, d.scroll)

	d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	bottom := d.scroll
	assert.Equal(t, d.maxScroll(), bottom)
	d.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, bottom, d.scroll, "down at bottom should clamp")
}

func TestLogHistoryDialog_RightClickCloses(t *testing.T) {
	d := NewLogHistoryDialog(0, [][]string{{"x"}}, "y")
	assert.True(t, d.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight}))
}

func TestLogHistoryDialog_ScrollHintReflectsScrollability(t *testing.T) {
	short := NewLogHistoryDialog(0, [][]string{{"x"}}, "y")
	assert.NotContains(t, short.View(100, 40), "scroll")

	var frames [][]string
	for i := 0; i < 30; i++ {
		frames = append(frames, []string{"row"})
	}
	long := NewLogHistoryDialog(0, frames, "final")
	assert.Contains(t, long.View(100, 40), "scroll")
}

func TestNewLogHistoryDialog_NoFramesStillShowsCommitted(t *testing.T) {
	d := NewLogHistoryDialog(3, nil, "only line")
	out := d.View(100, 40)
	assert.Contains(t, out, "committed")
	assert.Contains(t, out, "only line")
	assert.NotContains(t, out, "Frame 1")
}

// ─── model wiring ─────────────────────────────────────────────────────────────

func TestHandleLogLineHistory_OpensViewerWithFrames(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "run-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	m, _ = mustModel(m.handleLogLineHistory(uikit.LogLineHistoryMsg{
		RunID:     "run-1",
		Line:      4,
		Frames:    [][]string{{"a"}, {"b"}},
		Committed: "c",
	}))
	assert.True(t, m.dialogs.HasLogHistory(), "expected frame-history viewer to open")
}

func TestHandleLogLineHistory_EmptyFramesFlashesNoViewer(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "run-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	m, cmd := mustModel(m.handleLogLineHistory(uikit.LogLineHistoryMsg{RunID: "run-1", Line: 4}))
	assert.False(t, m.dialogs.HasLogHistory())
	assert.NotNil(t, cmd, "expected a flash cmd")
}

func TestHandleLogLineHistory_ErrorFlashesNoViewer(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "run-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	m, cmd := mustModel(m.handleLogLineHistory(uikit.LogLineHistoryMsg{
		RunID: "run-1", Line: 4, Err: assertAnError,
	}))
	assert.False(t, m.dialogs.HasLogHistory())
	assert.NotNil(t, cmd)
}

func TestHandleLogLineHistory_IgnoredWhenNotViewingRun(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "run-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev

	m, _ = mustModel(m.handleLogLineHistory(uikit.LogLineHistoryMsg{
		RunID: "other", Line: 4, Frames: [][]string{{"a"}},
	}))
	assert.False(t, m.dialogs.HasLogHistory())
}

func TestAnchorNav_MovesCursorAndEnterFetchesHistory(t *testing.T) {
	m := newTestModel(nil)
	m.streams = NewStreamManager(newDummyClient())
	run := &model.Run{ID: "run-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	// Two plain lines and one anchor carrying frame history.
	ev.Pane.AppendLogLine(0, "stdout", "plain a", 0)
	ev.Pane.AppendLogLine(1, "stdout", "bar 100%", 3)
	ev.Pane.AppendLogLine(2, "stdout", "plain b", 0)
	m.execView = &ev
	m.panelFocus = uikit.PanelMain
	m.execView.HeaderFocus = execlist.HeaderFocusNone

	// `]` jumps to the next anchor.
	newM, _, handled := handleKeyNextAnchor(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	assert.True(t, handled, "expected `]` to be handled when an anchor exists")
	m = newM
	absLine, _, ok := m.execView.Pane.CursorAnchor()
	assert.True(t, ok, "cursor should land on the anchor line")
	assert.Equal(t, int64(1), absLine)

	// Enter on the anchor issues the fetch command.
	newM, cmd, handled := m.openCursorFrameHistory()
	assert.True(t, handled)
	assert.NotNil(t, cmd, "expected a FetchLineHistory command")
	_ = newM
}

func TestAnchorNav_IgnoredWhenPaneNotFocused(t *testing.T) {
	m := newTestModel(nil)
	run := &model.Run{ID: "run-1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	ev.SetSize(80, 20)
	ev.Pane.AppendLogLine(0, "stdout", "bar", 2)
	m.execView = &ev
	m.panelFocus = uikit.PanelSidebar

	_, _, handled := handleKeyNextAnchor(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	assert.False(t, handled, "anchor nav must yield when the pane isn't focused")
}

func TestLogHistory_Itoa(t *testing.T) {
	assert.Equal(t, "0", itoa(0))
	assert.Equal(t, "7", itoa(7))
	assert.Equal(t, "128", itoa(128))
}

func TestLogHistoryDialog_ViewClipsWideRows(t *testing.T) {
	wide := strings.Repeat("X", 500)
	d := NewLogHistoryDialog(0, [][]string{{wide}}, "done")
	out := d.View(100, 40)
	// No single rendered line should blow past the screen width.
	for _, ln := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(ln), 100)
	}
}
