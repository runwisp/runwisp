// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logsearch

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/server"
)

func newTestModel(t *testing.T) Model {
	t.Helper()
	return New(apiclient.New("http://127.0.0.1:1", ""), "task1")
}

func TestNew_InitializesFields(t *testing.T) {
	m := newTestModel(t)
	if m.TaskName() != "task1" {
		t.Fatalf("TaskName: got %q want %q", m.TaskName(), "task1")
	}
	if m.hits != nil {
		t.Fatalf("Hits should be nil initially, got %#v", m.hits)
	}
	if m.Cursor() != -1 {
		t.Fatalf("Cursor should be -1 when no hits, got %d", m.Cursor())
	}
	if m.SelectedHit() != nil {
		t.Fatalf("SelectedHit should be nil initially")
	}
	if m.Regex() {
		t.Fatal("Regex should default to false")
	}
	if m.caseSensitive {
		t.Fatal("CaseSensitive should default to false")
	}
	if m.input.Value() != "" {
		t.Fatalf("Query should default to empty, got %q", m.input.Value())
	}
	if m.loading {
		t.Fatal("Loading should default to false")
	}
	if m.errMsg != "" {
		t.Fatalf("ErrorMessage should default to empty, got %q", m.errMsg)
	}
}

func TestUpdate_TabTogglesRegex(t *testing.T) {
	m := newTestModel(t)
	m2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !m2.Regex() {
		t.Fatal("expected regex on after tab")
	}
	m3, _ := m2.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m3.Regex() {
		t.Fatal("expected regex off after second tab")
	}
}

func TestUpdate_AltCTogglesCaseSensitive(t *testing.T) {
	m := newTestModel(t)
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c", Mod: tea.ModAlt})
	if !m2.caseSensitive {
		t.Fatal("expected case-sensitive on after alt+c")
	}
}

func TestUpdate_CursorMovement(t *testing.T) {
	m := newTestModel(t)
	m.hits = []server.LogSearchHit{
		{RunID: "r1", N: 1, Text: "one"},
		{RunID: "r2", N: 2, Text: "two"},
		{RunID: "r3", N: 3, Text: "three"},
	}
	m.input.SetValue("")
	// Use the "j"/"k" runes. Note: the Update switch on tea.KeyMsg.String()
	// translates KeyRunes("j") to "j".
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m2.Cursor() != 1 {
		t.Fatalf("Cursor after j: got %d want 1", m2.Cursor())
	}
	m3, _ := m2.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m3.Cursor() != 2 {
		t.Fatalf("Cursor after second j: got %d want 2", m3.Cursor())
	}
	// At end — should clamp.
	m4, _ := m3.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m4.Cursor() != 2 {
		t.Fatalf("Cursor clamp at end: got %d want 2", m4.Cursor())
	}
	m5, _ := m4.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m5.Cursor() != 1 {
		t.Fatalf("Cursor after k: got %d want 1", m5.Cursor())
	}
	m6, _ := m5.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m7, _ := m6.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m7.Cursor() != 0 {
		t.Fatalf("Cursor clamp at start: got %d want 0", m7.Cursor())
	}
}

func TestUpdate_ArrowKeys_AlsoMoveCursor(t *testing.T) {
	m := newTestModel(t)
	m.hits = []server.LogSearchHit{
		{RunID: "r1", N: 1, Text: "one"},
		{RunID: "r2", N: 2, Text: "two"},
	}
	m2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m2.Cursor() != 1 {
		t.Fatalf("Cursor after down: got %d want 1", m2.Cursor())
	}
	m3, _ := m2.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m3.Cursor() != 0 {
		t.Fatalf("Cursor after up: got %d want 0", m3.Cursor())
	}
}

func TestUpdate_EnterOnHitEmitsSelectMsg(t *testing.T) {
	m := newTestModel(t)
	m.hits = []server.LogSearchHit{{RunID: "rA", N: 42, Text: "hello"}}
	// Simulate the post-search state: a query was dispatched and results
	// arrived, so an Enter with the query unchanged selects the hit.
	m.input.SetValue("hello")
	m.lastSearched = "hello"
	m.searched = true
	m2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected select cmd")
	}
	sel, ok := cmd().(SelectMsg)
	if !ok {
		t.Fatalf("expected SelectMsg, got %T", cmd())
	}
	if sel.TaskName != "task1" || sel.RunID != "rA" || sel.Line != 42 {
		t.Fatalf("SelectMsg fields wrong: %+v", sel)
	}
	if m2.Cursor() != 0 {
		t.Fatalf("Cursor should still be 0, got %d", m2.Cursor())
	}
}

func TestUpdate_EnterNoHitsNoCmd(t *testing.T) {
	m := newTestModel(t)
	m.input.Blur()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected no cmd when no hits and input blurred")
	}
}

func TestUpdate_EnterWithQueryStartsSearch(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("needle")
	if !m.input.Focused() {
		t.Fatal("input should be focused after New()")
	}
	m2, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m2.loading {
		t.Fatal("expected Loading=true after starting search")
	}
	if cmd == nil {
		t.Fatal("expected search cmd")
	}
	// Execute the command — it will fail because the apiclient points at a
	// dead loopback port; we only care that it returns a resultsMsg.
	msg := cmd()
	rm, ok := msg.(resultsMsg)
	if !ok {
		t.Fatalf("expected resultsMsg, got %T", msg)
	}
	if rm.err == nil {
		t.Fatal("expected an error from the dead-port search")
	}
}

func TestUpdate_ResultsMsg_Success(t *testing.T) {
	m := newTestModel(t)
	m.loading = true
	m.errMsg = "prev error"
	hits := []server.LogSearchHit{{RunID: "r1", N: 7, Text: "x"}}
	m2, cmd := m.Update(resultsMsg{hits: hits})
	if cmd != nil {
		t.Fatal("expected no cmd on results")
	}
	if m2.loading {
		t.Fatal("expected Loading=false after results")
	}
	if m2.errMsg != "" {
		t.Fatalf("expected error cleared, got %q", m2.errMsg)
	}
	if len(m2.hits) != 1 || m2.Cursor() != 0 {
		t.Fatalf("hits/cursor wrong: hits=%d cursor=%d", len(m2.hits), m2.Cursor())
	}
}

func TestUpdate_ResultsMsg_Error(t *testing.T) {
	m := newTestModel(t)
	m.loading = true
	m.hits = []server.LogSearchHit{{RunID: "r1", N: 1}}
	m.cursor = 0
	m2, _ := m.Update(resultsMsg{err: errBoom{}})
	if m2.loading {
		t.Fatal("expected Loading=false after error")
	}
	if m2.errMsg != "boom" {
		t.Fatalf("ErrorMessage: got %q", m2.errMsg)
	}
	if m2.hits != nil {
		t.Fatal("hits should be cleared on error")
	}
}

func TestUpdate_PassesThroughToTextInput(t *testing.T) {
	m := newTestModel(t)
	// Typing a rune should reach the textinput component.
	m2, _ := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m2.input.Value() != "a" {
		t.Fatalf("Query after typing 'a': got %q", m2.input.Value())
	}
}

func TestView_RendersWithEmptyHits(t *testing.T) {
	m := newTestModel(t)
	out := m.View(80, 24)
	if !strings.Contains(out, "Search logs for task1") {
		t.Fatalf("expected title in view, got:\n%s", out)
	}
	if !strings.Contains(out, "substring") {
		t.Fatalf("expected substring flag in view, got:\n%s", out)
	}
}

func TestView_RendersLoading(t *testing.T) {
	m := newTestModel(t)
	m.loading = true
	out := m.View(80, 24)
	if !strings.Contains(out, "Searching") {
		t.Fatalf("expected Searching marker, got:\n%s", out)
	}
}

func TestView_RendersError(t *testing.T) {
	m := newTestModel(t)
	m.errMsg = "kaboom"
	out := m.View(80, 24)
	if !strings.Contains(out, "kaboom") {
		t.Fatalf("expected error text, got:\n%s", out)
	}
}

func TestView_RendersNoMatches(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("zzz")
	out := m.View(80, 24)
	if !strings.Contains(out, "No matches") {
		t.Fatalf("expected No matches, got:\n%s", out)
	}
}

func TestView_RendersHits_AndOverflow(t *testing.T) {
	m := newTestModel(t)
	// Build more hits than MaxVisibleHits to exercise the "more hits" branch.
	for i := 0; i < MaxVisibleHits+5; i++ {
		m.hits = append(m.hits, server.LogSearchHit{
			RunID: "01ARZ3NDEKTSV4RRFFQ69G5FA" + string(rune('A'+i%10)),
			N:     int64(i + 1),
			Text:  strings.Repeat("x", 250), // long line forces truncation
		})
	}
	out := m.View(80, 24)
	if !strings.Contains(out, fmt.Sprintf("of %d", MaxVisibleHits+5)) {
		t.Fatalf("expected windowed hit-count marker, got:\n%s", out)
	}
	if !strings.Contains(out, "▶") {
		t.Fatalf("expected selection cursor, got:\n%s", out)
	}
}

// TestView_WindowFollowsCursor exercises M10: with more hits than the visible
// window, the highlighted row must scroll into view instead of staying pinned
// to a fixed [0:MaxVisibleHits] slice.
func TestView_WindowFollowsCursor(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < MaxVisibleHits+8; i++ {
		m.hits = append(m.hits, server.LogSearchHit{
			RunID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
			N:     int64(i + 1),
			Text:  fmt.Sprintf("match-%02d", i),
		})
	}
	// Cursor well past the fixed window's bottom edge.
	m.cursor = MaxVisibleHits + 4
	out := m.View(120, 40)
	want := fmt.Sprintf("match-%02d", m.cursor)
	if !strings.Contains(out, want) {
		t.Fatalf("expected selected hit %q to be visible, got:\n%s", want, out)
	}
	// The selection cursor must render on the highlighted row.
	if !strings.Contains(out, "▶") {
		t.Fatalf("expected selection cursor in view, got:\n%s", out)
	}
}

// TestUpdate_EnterSelectsAfterSearch exercises M2: after a search runs and
// results arrive, pressing Enter again (query unchanged, input still focused
// as it always is in production) selects the highlighted hit rather than
// re-running the search.
func TestUpdate_EnterSelectsAfterSearch(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("needle")
	// First Enter dispatches the search; input stays focused.
	m2, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	// Results land.
	m3, _ := m2.Update(resultsMsg{hits: []server.LogSearchHit{{RunID: "rX", N: 9, Text: "hit"}}})
	// Second Enter must select, not search.
	_, cmd := m3.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a command from Enter")
	}
	sel, ok := cmd().(SelectMsg)
	if !ok {
		t.Fatalf("expected SelectMsg, got %T", cmd())
	}
	if sel.RunID != "rX" || sel.Line != 9 || sel.TaskName != "task1" {
		t.Fatalf("SelectMsg fields wrong: %+v", sel)
	}
}

func TestView_SmallWidth_ClampsTo40(t *testing.T) {
	m := newTestModel(t)
	out := m.View(20, 10)
	if out == "" {
		t.Fatal("expected non-empty view at small width")
	}
}

func TestView_CaseSensitiveFlagInFooter(t *testing.T) {
	m := newTestModel(t)
	m.caseSensitive = true
	m.regex = true
	out := m.View(80, 24)
	if !strings.Contains(out, "case-sensitive") {
		t.Fatalf("expected case-sensitive flag, got:\n%s", out)
	}
	if !strings.Contains(out, "regex") {
		t.Fatalf("expected regex flag, got:\n%s", out)
	}
}

// errBoom is a tiny error type for testing the error branch of resultsMsg.
type errBoom struct{}

func (errBoom) Error() string { return "boom" }
