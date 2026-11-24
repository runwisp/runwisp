// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/logfind"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/logsearch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openLog puts m in the log view of run r-1 with lines from..to loaded.
func openLog(m Model, from, to int64) Model {
	ev := execlist.NewExecView(&model.Run{ID: "r-1", TaskName: "t1", Status: model.PhaseEnded})
	m.execView = &ev
	m.panelFocus = uikit.PanelMain
	updated, _ := m.handleLogTailLoaded(uikit.LogTailLoadedMsg{RunID: "r-1", Lines: contiguousLines(from, to), Finalized: true})
	return updated.(Model)
}

func press(t *testing.T, m Model, k tea.KeyPressMsg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.handleKey(k)
	return updated.(Model), cmd
}

func TestFind_CtrlFOpensBarAndEscEditsThenClears(t *testing.T) {
	m := openLog(newTestModel(nil), 0, 20)
	m, _ = press(t, m, tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.NotNil(t, m.logFind)
	assert.True(t, m.logFind.Editing())
	assert.NotNil(t, m.execView.Pane.FindRanges)

	m, cmd := press(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	assert.NotNil(t, cmd, "typing goes to the find input and schedules a search")
	assert.True(t, m.logFind.Editing())

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, m.logFind.Editing())

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NotNil(t, m.logFind, "esc on an active find goes back to the query")
	assert.True(t, m.logFind.Editing())

	m, _ = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, m.logFind, "esc while editing clears the find")
	assert.Nil(t, m.execView.Pane.FindRanges)
	assert.NotNil(t, m.execView, "the log view stays open")
}

func TestFind_JumpMovesHighlight(t *testing.T) {
	m := openLog(newTestModel(nil), 0, 20)
	updated, _ := m.Update(logfind.JumpMsg{RunID: "r-1", Line: 12})
	got := updated.(Model)
	assert.Equal(t, int64(13), got.execView.Pane.HighlightLine, "line 12 is gutter line 13")
	assert.False(t, got.execView.Pane.Follow)
}

// A search hit older than the loaded tail used to be selected but never
// shown: the pane only held the tail and the jump silently did nothing.
func TestLogSearchSelect_HitBeforeLoadedTailPagesOlderLinesIn(t *testing.T) {
	m := openLog(newTestModel(nil), 2000, 2999)
	updated, cmd := m.handleLogSearchSelect(logsearch.SelectMsg{TaskName: "t1", RunID: "r-1", Line: 101})
	m = updated.(Model)
	require.NotNil(t, cmd, "expected a fetch for the older lines")
	assert.True(t, m.execView.LoadingOlder)
	assert.Equal(t, int64(101), m.pendingHighlight)

	updated, cmd = m.Update(uikit.LogOlderLoadedMsg{RunID: "r-1", Lines: contiguousLines(50, 1999)})
	m = updated.(Model)
	assert.Nil(t, cmd)
	assert.Equal(t, int64(101), m.execView.Pane.HighlightLine)
	assert.Equal(t, "l100", m.execView.Pane.Lines[int(m.execView.Pane.HighlightLine)-m.execView.Pane.FirstLoadedLine-1].Text)
	assert.Zero(t, m.pendingHighlight)
}
