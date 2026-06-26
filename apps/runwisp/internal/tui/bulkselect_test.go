// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
)

// runListModel returns a model focused on the executions list with the given
// run IDs seeded into the window.
func runListModel(t *testing.T, ids ...string) Model {
	t.Helper()
	m := newTestModelWithClient(nil)
	m.focusMainPanel() // PanelMain + homeCursor = -1
	items := make([]uikit.ExecListItem, len(ids))
	for i, id := range ids {
		items[i] = uikit.ExecListItem{Run: model.Run{ID: id, TaskName: "task"}}
	}
	m.execWindow.ApplyFetch(items, 0, len(ids))
	m.execList.SetFocused(true)
	if !m.runListFocused() {
		t.Fatal("precondition: expected the run list to be focused")
	}
	return m
}

func TestRunListFocused_FalseWhenExecViewOpen(t *testing.T) {
	m := runListModel(t, "a")
	ev := execlist.NewExecView(&model.Run{ID: "a", TaskName: "task"})
	m.execView = &ev
	if m.runListFocused() {
		t.Fatal("an open exec view must take focus away from the run list")
	}
}

func TestHandleRunListSelectionKey_SpaceToggles(t *testing.T) {
	m := runListModel(t, "a", "b")
	got, _, handled := m.handleRunListSelectionKey(tea.KeyMsg{Type: tea.KeySpace})
	if !handled {
		t.Fatal("space should be handled while the run list is focused")
	}
	if got.execList.SelectionCount() != 1 {
		t.Fatalf("expected one selected after space, got %d", got.execList.SelectionCount())
	}
}

func TestHandleRunListSelectionKey_SelectAll(t *testing.T) {
	m := runListModel(t, "a", "b", "c")
	got, _, handled := m.handleRunListSelectionKey(keyRunes("a"))
	if !handled {
		t.Fatal("`a` should select all when rows exist")
	}
	sel, ok := got.execList.SelectionSelector()
	if !ok || !sel.MatchAll {
		t.Fatalf("expected a MatchAll selector after select-all, got ok=%v matchAll=%v", ok, sel.MatchAll)
	}
}

func TestHandleRunListSelectionKey_SelectAllFallsThroughWhenEmpty(t *testing.T) {
	m := runListModel(t) // no rows
	if _, _, handled := m.handleRunListSelectionKey(keyRunes("a")); handled {
		t.Fatal("`a` must fall through when there are no rows (so it stays free)")
	}
}

func TestHandleRunListSelectionKey_ActionsRequireSelection(t *testing.T) {
	m := runListModel(t, "a")
	// With nothing selected, d/c/e/esc are not owned by the selection handler.
	for _, k := range []string{"d", "c", "e", "esc"} {
		if _, _, handled := m.handleRunListSelectionKey(keyRunes(k)); handled {
			t.Fatalf("%q must fall through when nothing is selected", k)
		}
	}
}

func TestHandleRunListSelectionKey_EscClearsSelection(t *testing.T) {
	m := runListModel(t, "a")
	m.execList.ToggleSelectCursor()
	got, _, handled := m.handleRunListSelectionKey(keyRunes("esc"))
	if !handled {
		t.Fatal("esc should clear an active selection")
	}
	if got.execList.SelectionActive() {
		t.Fatal("expected the selection cleared after esc")
	}
}

func TestHandleRunListSelectionKey_DeleteDispatchesAndClears(t *testing.T) {
	m := runListModel(t, "a", "b")
	m.execList.ToggleSelectCursor() // select "a"
	got, cmd, handled := m.handleRunListSelectionKey(keyRunes("d"))
	if !handled {
		t.Fatal("d should delete the selection")
	}
	if cmd == nil {
		t.Fatal("expected a delete command")
	}
	if got.execList.SelectionActive() {
		t.Fatal("the selection must clear once the bulk action fires")
	}
}

func TestHandleRunListSelectionKey_RerunAndCancelDispatch(t *testing.T) {
	for _, k := range []string{"e", "c"} {
		m := runListModel(t, "a")
		m.execList.ToggleSelectCursor()
		_, cmd, handled := m.handleRunListSelectionKey(keyRunes(k))
		if !handled || cmd == nil {
			t.Fatalf("%q should dispatch a bulk command, got handled=%v cmd=%v", k, handled, cmd)
		}
	}
}

func TestHandleKeyRoutesSelectionKeysWhenRunListFocused(t *testing.T) {
	m := runListModel(t, "a", "b")
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	got, ok := updated.(Model)
	if !ok {
		t.Fatal("handleKey must return a Model")
	}
	if got.execList.SelectionCount() != 1 {
		t.Fatalf("space through handleKey should toggle a selection, got %d", got.execList.SelectionCount())
	}
}

func TestHandleBulkDeleteResult_IDsArmsUndo(t *testing.T) {
	m := newTestModelWithClient(nil)
	updated, cmd := m.handleBulkDeleteResult(uikit.BulkDeleteResultMsg{
		Affected: 2,
		Restore:  model.RunSelector{IDs: []string{"a", "b"}},
	})
	if cmd == nil {
		t.Fatal("expected a batched command (refetch + undo toast)")
	}
	got := updated.(Model)
	if got.dialogs.TakeUndo() == nil {
		t.Fatal("an IDs-mode bulk delete must arm an undo")
	}
}

func TestHandleBulkDeleteResult_MatchAllHasNoUndo(t *testing.T) {
	m := newTestModelWithClient(nil)
	updated, _ := m.handleBulkDeleteResult(uikit.BulkDeleteResultMsg{
		Affected: 5,
		Restore:  model.RunSelector{MatchAll: true},
	})
	got := updated.(Model)
	if got.dialogs.TakeUndo() != nil {
		t.Fatal("a MatchAll bulk delete must not arm an undo (could over-restore)")
	}
}

func TestHandleBulkDeleteResult_ErrorFlashesNoUndo(t *testing.T) {
	m := newTestModelWithClient(nil)
	updated, _ := m.handleBulkDeleteResult(uikit.BulkDeleteResultMsg{
		Restore: model.RunSelector{IDs: []string{"a"}},
		Err:     errors.New("boom"),
	})
	got := updated.(Model)
	if got.dialogs.TakeUndo() != nil {
		t.Fatal("a failed bulk delete must not arm an undo")
	}
}

func keyRunes(s string) tea.KeyMsg {
	if s == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}
