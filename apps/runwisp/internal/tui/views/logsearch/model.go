// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package logsearch is the TUI overlay for the task-wide log-search feature.
// It owns input state, kicks off REST calls through the supplied client, and
// renders a flat scrollable hit list. Selecting a hit fires a Bubble Tea
// message that the parent model translates into "open this run, scroll to
// that line".
package logsearch

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/server"
)

const (
	// MaxVisibleHits caps the rendered result list. Hits beyond this are
	// still in the model; the user can scroll to them.
	MaxVisibleHits = 12
)

// SelectMsg fires when the user presses Enter on a hit. The parent model
// opens the run and asks the pane to highlight the line.
type SelectMsg struct {
	TaskName string
	RunID    string
	Line     int64
}

// resultsMsg carries the API response back into the Bubble Tea Update loop.
type resultsMsg struct {
	hits []server.LogSearchHit
	err  error
}

// Model is the search overlay's state. Owned by the root TUI Model; one
// instance per "search session" — destroyed on Esc.
type Model struct {
	taskName      string
	input         textinput.Model
	regex         bool
	caseSensitive bool
	hits          []server.LogSearchHit
	cursor        int // selected hit index
	loading       bool
	errMsg        string
	client        *apiclient.Client
}

// New creates a fresh overlay for the given task.
func New(client *apiclient.Client, taskName string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search…"
	ti.CharLimit = 1024
	ti.Width = 48
	ti.Focus()
	return Model{
		taskName: taskName,
		input:    ti,
		client:   client,
	}
}

// TaskName returns the task the overlay is scoped to.
func (m *Model) TaskName() string { return m.taskName }

// Hits returns the current result set (read-only — useful for tests).
func (m *Model) Hits() []server.LogSearchHit { return m.hits }

// Cursor returns the currently selected hit index, or -1 if there are no
// hits.
func (m *Model) Cursor() int {
	if len(m.hits) == 0 {
		return -1
	}
	return m.cursor
}

// SelectedHit returns the highlighted result, if any.
func (m *Model) SelectedHit() *server.LogSearchHit {
	if len(m.hits) == 0 {
		return nil
	}
	h := m.hits[m.cursor]
	return &h
}

// Update consumes one key event. Returns the new model and an optional
// command (an async search request, or a SelectMsg when the user picks a
// hit). It does NOT consume keys it cannot interpret (e.g. window resizes)
// — the parent's Update routes those.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case resultsMsg:
		m.loading = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.hits = nil
			m.cursor = 0
			return m, nil
		}
		m.errMsg = ""
		m.hits = msg.hits
		m.cursor = 0
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.input.Focused() && m.input.Value() != "" {
				return m.startSearch()
			}
			if h := m.SelectedHit(); h != nil {
				return m, func() tea.Msg {
					return SelectMsg{TaskName: m.taskName, RunID: h.RunID, Line: h.N}
				}
			}
			return m, nil
		case "tab":
			m.regex = !m.regex
			return m, nil
		case "alt+c":
			m.caseSensitive = !m.caseSensitive
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.hits)-1 {
				m.cursor++
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// Regex reports whether the overlay is in regex mode.
func (m *Model) Regex() bool { return m.regex }

// CaseSensitive reports whether the overlay is matching with case
// sensitivity.
func (m *Model) CaseSensitive() bool { return m.caseSensitive }

// Query returns the current query string.
func (m *Model) Query() string { return m.input.Value() }

// Loading reports whether a request is in flight.
func (m *Model) Loading() bool { return m.loading }

// ErrorMessage returns any error message from the last failed request.
func (m *Model) ErrorMessage() string { return m.errMsg }

// startSearch fires off a search and returns the model with loading=true
// plus the async command. Extracted so the Update switch stays short.
func (m Model) startSearch() (Model, tea.Cmd) {
	m.loading = true
	m.errMsg = ""
	client := m.client
	taskName := m.taskName
	opts := apiclient.SearchLogsOptions{
		Query:         m.input.Value(),
		Regex:         m.regex,
		CaseSensitive: m.caseSensitive,
	}
	return m, func() tea.Msg {
		body, err := client.SearchLogs(taskName, opts)
		if err != nil {
			return resultsMsg{err: err}
		}
		return resultsMsg{hits: body.Hits}
	}
}
