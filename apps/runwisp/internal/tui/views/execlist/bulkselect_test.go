// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

import (
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// selectList builds a focused list backed by a window holding the given run IDs.
func selectList(ids ...string) ExecList {
	w := NewExecWindow(nil)
	items := make([]uikit.ExecListItem, len(ids))
	for i, id := range ids {
		items[i] = uikit.ExecListItem{Run: model.Run{ID: id, TaskName: "task"}}
	}
	w.ApplyFetch(items, 0, len(ids))
	l := NewExecList(w)
	l.SetFocused(true)
	return l
}

func TestToggleSelectCursor_AddsAndRemoves(t *testing.T) {
	l := selectList("a", "b", "c")
	l.cursor = 1

	l.ToggleSelectCursor()
	if !l.SelectionActive() || l.SelectionCount() != 1 || !l.isRowSelected("b") {
		t.Fatalf("expected run b selected once, got active=%v count=%d", l.SelectionActive(), l.SelectionCount())
	}

	l.ToggleSelectCursor()
	if l.SelectionActive() {
		t.Fatal("toggling the same row off should clear the selection")
	}
}

func TestToggleSelectCursor_NoLoadedRowIsNoop(t *testing.T) {
	l := selectList() // empty window
	l.ToggleSelectCursor()
	if l.SelectionActive() {
		t.Fatal("toggling with no loaded row must not select anything")
	}
}

func TestSelectionSelector_IDsModeIsSorted(t *testing.T) {
	l := selectList("c", "a", "b")
	for i := 0; i < 3; i++ {
		l.cursor = i
		l.ToggleSelectCursor()
	}

	sel, ok := l.SelectionSelector()
	if !ok {
		t.Fatal("expected a selector for a non-empty selection")
	}
	if sel.MatchAll {
		t.Fatal("an explicit selection must not be MatchAll")
	}
	want := []string{"a", "b", "c"}
	if len(sel.IDs) != len(want) {
		t.Fatalf("expected %d ids, got %v", len(want), sel.IDs)
	}
	for i := range want {
		if sel.IDs[i] != want[i] {
			t.Fatalf("ids must be sorted for determinism, got %v", sel.IDs)
		}
	}
	if err := sel.Validate(); err != nil {
		t.Fatalf("IDs selector should validate: %v", err)
	}
}

func TestSelectAllMatching_UsesMatchAllSelectorWithActiveFilter(t *testing.T) {
	l := selectList("a", "b")
	l.window.statusFilter = "failed"
	l.window.filterTask = "task"

	l.SelectAllMatching()
	if l.SelectionCount() != 2 {
		t.Fatalf("select-all count should be the matching total (2), got %d", l.SelectionCount())
	}
	sel, ok := l.SelectionSelector()
	if !ok || !sel.MatchAll {
		t.Fatalf("expected a MatchAll selector, got ok=%v matchAll=%v", ok, sel.MatchAll)
	}
	// The "failed" bucket expands to the full wire set (matching the web UI), so
	// the MatchAll selector carries that comma-joined status set, not the label.
	if sel.Filter.Status != statusFilterWire["failed"] || sel.Filter.TaskName != "task" {
		t.Fatalf("MatchAll selector must carry the active filter, got %+v", sel.Filter)
	}
	if err := sel.Validate(); err != nil {
		t.Fatalf("MatchAll selector should validate: %v", err)
	}
}

func TestToggleAfterSelectAll_DropsMatchAll(t *testing.T) {
	l := selectList("a", "b")
	l.SelectAllMatching()
	l.cursor = 0
	l.ToggleSelectCursor()

	sel, ok := l.SelectionSelector()
	if !ok || sel.MatchAll {
		t.Fatalf("toggling after select-all must yield an explicit selector, got matchAll=%v", sel.MatchAll)
	}
	if len(sel.IDs) != 1 || sel.IDs[0] != "a" {
		t.Fatalf("expected just run a selected, got %v", sel.IDs)
	}
}

func TestClearSelection_Empties(t *testing.T) {
	l := selectList("a")
	l.ToggleSelectCursor()
	l.ClearSelection()

	if l.SelectionActive() {
		t.Fatal("expected no selection after clear")
	}
	if _, ok := l.SelectionSelector(); ok {
		t.Fatal("a cleared selection must not produce a selector")
	}
}

func TestRowPrefix_ReflectsSelectionState(t *testing.T) {
	l := selectList("a", "b")
	if l.rowPrefix("a") != "  " {
		t.Fatalf("expected a plain 2-cell indent with no selection, got %q", l.rowPrefix("a"))
	}

	l.cursor = 0
	l.ToggleSelectCursor()
	if l.rowPrefix("a") != checkboxChecked+" " {
		t.Fatalf("expected the checked glyph for a selected row, got %q", l.rowPrefix("a"))
	}
	if l.rowPrefix("b") != checkboxEmpty+" " {
		t.Fatalf("expected the empty glyph for an unselected row while selecting, got %q", l.rowPrefix("b"))
	}
}

func TestView_FooterShowsSelectionCount(t *testing.T) {
	l := selectList("a", "b", "c")
	l.SetSize(80, 12)
	l.cursor = 0
	l.ToggleSelectCursor()

	if out := l.View(); !strings.Contains(out, "1 selected") {
		t.Fatalf("expected the footer to show the selection count, got:\n%s", out)
	}
}

func TestCurrentFilter_MirrorsWindowState(t *testing.T) {
	w := NewExecWindow(nil)
	w.statusFilter = "running"
	w.filterTask = "deploy"
	f := w.CurrentFilter()
	// CurrentFilter expands the bucket label to the wire status set sent to the
	// server (the web UI's "Running" bucket = pending + running).
	if f.Status != statusFilterWire["running"] || f.TaskName != "deploy" {
		t.Fatalf("CurrentFilter must mirror the window's filter, got %+v", f)
	}
}
