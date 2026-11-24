// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logfind

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestFind() Model {
	return New(nil, "task", "run-1")
}

func typeQuery(m Model, q string) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, r := range q {
		m, cmd = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m, cmd
}

func jumpLine(t *testing.T, cmd tea.Cmd) int64 {
	t.Helper()
	require.NotNil(t, cmd)
	msg, ok := cmd().(JumpMsg)
	require.True(t, ok, "expected a JumpMsg")
	assert.Equal(t, "run-1", msg.RunID)
	return msg.Line
}

func TestFind_StaleResultsAreDropped(t *testing.T) {
	m, _ := typeQuery(newTestFind(), "err")
	staleSeq := m.seq
	m, _ = typeQuery(m, "or")
	m, cmd := m.Update(resultsMsg{seq: staleSeq, lines: []int64{1}, lastHit: 1})
	assert.Nil(t, cmd)
	assert.Empty(t, m.matches)

	m, _ = m.Update(debounceMsg{seq: staleSeq})
	assert.True(t, m.loading, "a stale debounce must not start a search")
}

func TestFind_LandsOnFirstMatch(t *testing.T) {
	m, _ := typeQuery(newTestFind(), "x")
	m, cmd := m.Update(resultsMsg{seq: m.seq, lines: []int64{30, 5, 12}, lastHit: 30})
	assert.Equal(t, int64(5), jumpLine(t, cmd))
	assert.Equal(t, []int64{5, 12, 30}, m.matches)
}

func TestFind_HomeEndAndPaging(t *testing.T) {
	m, _ := typeQuery(newTestFind(), "x")
	lines := make([]int64, 300)
	for i := range lines {
		lines[i] = int64(i)
	}
	m, _ = m.Update(resultsMsg{seq: m.seq, lines: lines, lastHit: 299})
	key := func(code rune) int64 {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyPressMsg{Code: code})
		return jumpLine(t, cmd)
	}
	assert.Equal(t, int64(30), key(tea.KeyPgDown), "a page is a tenth of 300 matches")
	assert.Equal(t, int64(0), key(tea.KeyPgUp))
	assert.Equal(t, int64(0), key(tea.KeyPgUp), "paging stops at the first match")
	assert.Equal(t, int64(299), key(tea.KeyEnd))
	assert.Equal(t, int64(299), key(tea.KeyPgDown), "and at the last")
	assert.Equal(t, int64(0), key(tea.KeyHome))

	few, _ := typeQuery(newTestFind(), "x")
	few, _ = few.Update(resultsMsg{seq: few.seq, lines: []int64{1, 2, 3}, lastHit: 3})
	assert.Equal(t, int64(3), jumpLine(t, few.Page(1)), "a page is at least 10 matches")
}

func TestFind_NextPrevWrapAndIngestDedupes(t *testing.T) {
	m, _ := typeQuery(newTestFind(), "boom")
	m, _ = m.Update(resultsMsg{seq: m.seq, lines: []int64{2, 4}, lastHit: 4})
	m.Ingest(4, "boom again")
	m.Ingest(9, "\x1b[1mBOOM\x1b[0m")
	m.Ingest(10, "fine")
	assert.Equal(t, []int64{2, 4, 9}, m.matches)

	assert.Equal(t, int64(2), m.Current())
	assert.Equal(t, int64(4), jumpLine(t, m.Next()))
	assert.Equal(t, int64(9), jumpLine(t, m.Next()))
	assert.Equal(t, int64(2), jumpLine(t, m.Next()), "next wraps to the first")
	assert.Equal(t, int64(9), jumpLine(t, m.Prev()), "prev wraps to the last")
}

func TestFind_CapStopsLiveMatching(t *testing.T) {
	m, _ := typeQuery(newTestFind(), "x")
	m.Ingest(500, "x streamed past the cap")
	m, _ = m.Update(resultsMsg{seq: m.seq, lines: []int64{1, 2}, lastHit: 2, capped: true})
	assert.Equal(t, []int64{1, 2}, m.matches, "nothing past the scanned range survives a cap")
	m.Ingest(600, "x")
	assert.Equal(t, []int64{1, 2}, m.matches)
	assert.Equal(t, "1 of 2+", m.statusText())
}

func TestFind_EnterStopsEditingAndOptionsRecompile(t *testing.T) {
	m, _ := typeQuery(newTestFind(), "a.c")
	assert.Equal(t, [][]int(nil), m.Ranges("abc"), "a literal query doesn't treat . as a wildcard")
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	assert.True(t, m.regex)
	assert.Equal(t, [][]int{{0, 3}}, m.Ranges("ABC"), "regex, case-insensitive by default")
	m, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt})
	assert.Nil(t, m.Ranges("ABC"), "alt+c makes it case-sensitive")

	m, _ = m.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	assert.Equal(t, "invalid regex", m.statusText())

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.False(t, m.Editing())
}

// The bar sits in the help-bar row: a cell drawn without a background shows
// the terminal's own colour through it.
func TestFind_ViewPaintsEveryCell(t *testing.T) {
	m, _ := typeQuery(newTestFind(), "widen")
	m, _ = m.Update(resultsMsg{seq: m.seq, lines: []int64{3}, lastHit: 3})
	for _, view := range []string{m.View(120), func() string { m.editing = false; return m.View(120) }()} {
		assert.NotContains(t, view, "\x1b[m ", "a space right after a reset has no background")
		assert.False(t, strings.HasPrefix(view, " "), "the leading space needs a background")
	}
}
