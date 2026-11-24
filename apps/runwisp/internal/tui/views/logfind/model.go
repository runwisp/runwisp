// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logfind is the TUI's "find in this run's log" bar. The log pane only
// holds a window of the log, so matches come from the daemon's search over the
// whole file; lines that stream in afterwards are matched here and appended.
// The bar asks the parent to reveal a line with JumpMsg and draws itself in
// the help-bar row.
package logfind

import (
	"context"
	"regexp"
	"slices"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logsearch"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

const (
	debounce = 150 * time.Millisecond
	pageSize = 1000
	// maxPages caps one find at 10k matches; past that the count shows "+"
	// and live matching stops rather than leave a silent gap.
	maxPages = 10
)

// JumpMsg asks the parent to show line Line (0-based) of run RunID.
type JumpMsg struct {
	RunID string
	Line  int64
}

type debounceMsg struct{ seq int }

type resultsMsg struct {
	seq     int
	lines   []int64
	lastHit int64
	capped  bool
	err     error
}

// Model is one find session on one run. Owned by the root model; dropped when
// the find is cleared or the log view closes.
type Model struct {
	client   *apiclient.Client
	taskName string
	runID    string

	input         textinput.Model
	regex         bool
	caseSensitive bool
	editing       bool

	re      *regexp.Regexp // nil while the query is empty or invalid
	matches []int64        // matching lines, ascending
	current int64          // selected match, -1 for none
	loading bool
	errMsg  string
	capped  bool
	seq     int
}

// New opens the find bar for one run, ready for typing.
func New(client *apiclient.Client, taskName, runID string) Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "find in this log"
	ti.CharLimit = 1024
	ti.SetWidth(32)
	ti.SetStyles(uikit.InputStyles(uikit.ColorBgLight))
	ti.Focus()
	return Model{
		client:   client,
		taskName: taskName,
		runID:    runID,
		input:    ti,
		editing:  true,
		current:  -1,
	}
}

// Editing reports whether the query input has the keyboard.
func (m Model) Editing() bool { return m.editing }

// RunID is the run this find belongs to.
func (m Model) RunID() string { return m.runID }

// Current is the selected match (0-based line), or -1.
func (m Model) Current() int64 { return m.current }

// Edit gives the keyboard back to the query input.
func (m *Model) Edit() tea.Cmd {
	m.editing = true
	m.input.CursorEnd()
	return m.input.Focus()
}

// Ranges returns the [start, end) byte offsets of matches in text with its
// ANSI codes removed, for the log pane to highlight. Nil when nothing matches.
func (m Model) Ranges(text string) [][]int {
	if m.re == nil {
		return nil
	}
	return m.re.FindAllStringIndex(logsearch.VisibleText(text), -1)
}

// Ingest matches a line that just streamed in. It never moves the selection.
func (m *Model) Ingest(n int64, text string) {
	if m.re == nil || m.capped || m.errMsg != "" {
		return
	}
	if !m.re.MatchString(logsearch.VisibleText(text)) {
		return
	}
	if i, found := slices.BinarySearch(m.matches, n); !found {
		m.matches = slices.Insert(m.matches, i, n)
	}
}

// Next selects the match after the current one, wrapping to the first.
func (m *Model) Next() tea.Cmd {
	if len(m.matches) == 0 {
		return nil
	}
	i, _ := slices.BinarySearch(m.matches, m.current+1)
	if m.current < 0 || i >= len(m.matches) {
		i = 0
	}
	return m.selectLine(m.matches[i])
}

// Prev selects the match before the current one, wrapping to the last.
func (m *Model) Prev() tea.Cmd {
	if len(m.matches) == 0 {
		return nil
	}
	i, _ := slices.BinarySearch(m.matches, m.current)
	if m.current < 0 || i == 0 {
		i = len(m.matches)
	}
	return m.selectLine(m.matches[i-1])
}

// First selects the first match.
func (m *Model) First() tea.Cmd {
	if len(m.matches) == 0 {
		return nil
	}
	return m.selectLine(m.matches[0])
}

// Last selects the last match.
func (m *Model) Last() tea.Cmd {
	if len(m.matches) == 0 {
		return nil
	}
	return m.selectLine(m.matches[len(m.matches)-1])
}

// Page jumps dir pages of matches, stopping at either end. A page is 10
// matches or a tenth of them, whichever is more.
func (m *Model) Page(dir int) tea.Cmd {
	if len(m.matches) == 0 {
		return nil
	}
	step := max(10, (len(m.matches)+9)/10)
	i, _ := slices.BinarySearch(m.matches, m.current)
	i = min(max(i+dir*step, 0), len(m.matches)-1)
	return m.selectLine(m.matches[i])
}

// NavKey moves through the matches for home/end/pgup/pgdown, which work
// whether or not the query is being edited. ok is false for any other key.
func (m *Model) NavKey(key string) (cmd tea.Cmd, ok bool) {
	switch key {
	case "home":
		return m.First(), true
	case "end":
		return m.Last(), true
	case "pgup":
		return m.Page(-1), true
	case "pgdown":
		return m.Page(1), true
	}
	return nil, false
}

func (m *Model) selectLine(n int64) tea.Cmd {
	m.current = n
	runID := m.runID
	return func() tea.Msg { return JumpMsg{RunID: runID, Line: n} }
}

// Update handles keys while editing, plus the bar's own async messages.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case debounceMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		return m, m.searchCmd(msg.seq)
	case resultsMsg:
		return m.handleResults(msg)
	case tea.KeyPressMsg:
		if cmd, ok := m.NavKey(msg.String()); ok {
			return m, cmd
		}
		if m.editing {
			return m.handleKey(msg)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "enter":
		m.editing = false
		m.input.Blur()
		if m.current < 0 {
			return m, m.Next()
		}
		return m, m.selectLine(m.current)
	case "tab":
		m.regex = !m.regex
		return m.queryChanged()
	case "alt+c":
		m.caseSensitive = !m.caseSensitive
		return m.queryChanged()
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() == before {
		return m, cmd
	}
	next, search := m.queryChanged()
	return next, tea.Batch(cmd, search)
}

// queryChanged recompiles the query, clears the old matches and schedules a
// search once typing pauses.
func (m Model) queryChanged() (Model, tea.Cmd) {
	m.seq++
	m.matches = nil
	m.current = -1
	m.capped = false
	m.errMsg = ""
	m.re = nil
	m.loading = false
	q := m.input.Value()
	if q == "" {
		return m, nil
	}
	re, err := compile(q, m.regex, m.caseSensitive)
	if err != nil {
		m.errMsg = "invalid regex"
		return m, nil
	}
	m.re = re
	m.loading = true
	seq := m.seq
	return m, tea.Tick(debounce, func(time.Time) tea.Msg { return debounceMsg{seq: seq} })
}

// compile turns a query into the regexp used for live matching and
// highlighting: a literal substring unless regex, case-insensitive unless
// caseSensitive, the same reading the daemon's search gives it.
func compile(q string, regex, caseSensitive bool) (*regexp.Regexp, error) {
	if !regex {
		q = regexp.QuoteMeta(q)
	}
	if !caseSensitive {
		q = "(?i)" + q
	}
	return regexp.Compile(q)
}

// searchCmd pages through the daemon's search of this run in one goroutine.
func (m Model) searchCmd(seq int) tea.Cmd {
	client, taskName := m.client, m.taskName
	opts := apiclient.SearchLogsOptions{
		Query:         m.input.Value(),
		Regex:         m.regex,
		CaseSensitive: m.caseSensitive,
		RunID:         m.runID,
		Limit:         pageSize,
	}
	return func() tea.Msg {
		res := resultsMsg{seq: seq, lastHit: -1}
		for range maxPages {
			body, err := client.SearchLogs(context.Background(), taskName, opts)
			if err != nil {
				res.err = err
				return res
			}
			for _, hit := range body.Items {
				res.lines = append(res.lines, hit.N)
				res.lastHit = max(res.lastHit, hit.N)
			}
			opts.Cursor = body.NextCursor
			if opts.Cursor == "" {
				return res
			}
		}
		res.capped = true
		return res
	}
}

func (m Model) handleResults(msg resultsMsg) (Model, tea.Cmd) {
	if msg.seq != m.seq {
		return m, nil
	}
	m.loading = false
	if msg.err != nil {
		m.errMsg = msg.err.Error()
		m.matches = nil
		return m, nil
	}
	slices.Sort(msg.lines)
	m.matches = mergeLines(m.matches, msg.lines)
	if msg.capped {
		// Lines streamed in during the search may sit past a gap the cap left
		// unscanned; keep only what the search itself covered.
		m.matches = slices.DeleteFunc(m.matches, func(n int64) bool { return n > msg.lastHit })
		m.capped = true
	}
	return m, m.First()
}

// mergeLines merges two ascending line lists, dropping duplicates.
func mergeLines(a, b []int64) []int64 {
	return slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(a), b...))))
}
