// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// closes feeds msg to d and reports whether the dialog asked to close.
func closes(d dialog, msg tea.Msg) bool {
	_, closed := d.Update(msg)
	return closed
}

// oneOfEachDialog returns a dialog for every kind, in precedence order.
func oneOfEachDialog() []dialog {
	return []dialog{
		dlgConfirm:    NewConfirmDialog("Confirm", "Sure?", func() tea.Msg { return nil }),
		dlgParamForm:  NewParamFormDialog("deploy", []model.TaskParam{{Kind: model.ParamArg, Key: "branch"}}, func(map[string]*string) tea.Cmd { return nil }),
		dlgRunParams:  NewRunParamsDialog("task", map[string]string{"k": "v"}),
		dlgCopy:       NewCopyDialog("Title", "value"),
		dlgLogHistory: NewLogHistoryDialog(0, [][]string{{"frame"}}, "committed"),
		dlgNewRelease: NewNewReleaseDialog("1.0.0", "v2.0.0"),
		dlgTaskDetail: NewTaskDetailDialog("alpha", &model.Task{Name: "alpha"}),
		dlgRunDetail:  NewRunDetailDialog(&model.Run{ID: "r1", TaskName: "t1"}, false, 1),
		dlgHelp:       &HelpDialog{},
	}
}

func TestDialogManager_ShowHasDismiss(t *testing.T) {
	for kind, d := range oneOfEachDialog() {
		var dm DialogManager
		k := dialogKind(kind)
		if dm.Has(k) {
			t.Fatalf("kind %d: expected no dialog initially", k)
		}
		dm.Show(k, d)
		if !dm.Has(k) {
			t.Fatalf("kind %d: expected dialog after Show", k)
		}
		if dm.RenderOverlays("base", 80, 24) == "base" {
			t.Fatalf("kind %d: expected RenderOverlays to render the dialog", k)
		}
		dm.Dismiss(k)
		if dm.Has(k) {
			t.Fatalf("kind %d: expected no dialog after Dismiss", k)
		}
	}
}

// Every dialog closes on Esc (the run inspector and task inspector included).
func TestDialogs_CloseOnEsc(t *testing.T) {
	for kind, d := range oneOfEachDialog() {
		if !closes(d, tea.KeyPressMsg{Code: tea.KeyEscape}) {
			t.Fatalf("kind %d: expected Esc to close the dialog", kind)
		}
	}
}

func TestDialogManager_TopFollowsPrecedence(t *testing.T) {
	var dm DialogManager
	all := oneOfEachDialog()
	// Open in reverse so insertion order can't be what decides.
	for k := len(all) - 1; k >= 0; k-- {
		dm.Show(dialogKind(k), all[k])
	}
	for want := range all {
		got, d, ok := dm.top()
		if !ok || got != dialogKind(want) || d != all[want] {
			t.Fatalf("expected kind %d on top, got %d (ok=%v)", want, got, ok)
		}
		dm.Dismiss(got)
	}
	if _, _, ok := dm.top(); ok {
		t.Fatal("expected no dialog after dismissing all")
	}
}

func TestDialogManager_ApplyTaskSummary(t *testing.T) {
	var dm DialogManager
	// A no-op with no inspector open.
	dm.ApplyTaskSummary(uikit.TaskSummaryMsg{TaskName: "alpha"})

	d := NewTaskDetailDialog("alpha", &model.Task{Name: "alpha"})
	dm.Show(dlgTaskDetail, d)
	dm.ApplyTaskSummary(uikit.TaskSummaryMsg{TaskName: "alpha", Total: 5, Success: 4, Failed: 1})
	if !d.health.loaded || d.health.summary.Total != 5 {
		t.Fatalf("expected the open inspector to receive the summary, got %+v", d.health)
	}
}

func TestDialogManager_Flash(t *testing.T) {
	var dm DialogManager

	msg, ok := dm.FlashActive()
	if ok {
		t.Fatalf("expected no flash initially, got %q", msg)
	}

	dm.Flash("Hello!", time.Hour) // long duration → still active immediately

	msg, ok = dm.FlashActive()
	if !ok || msg != "Hello!" {
		t.Fatalf("expected flash 'Hello!', got ok=%v msg=%q", ok, msg)
	}

	// Rewind expiry to the past instead of sleeping; the public observation
	// of "expired" is identical and the test is deterministic.
	dm.flashExpiry = time.Now().Add(-time.Second)

	msg, ok = dm.FlashActive()
	if ok {
		t.Fatalf("expected flash expired, got %q", msg)
	}
}

func TestDialogManager_FlashErrorFlagResetsOnNextFlash(t *testing.T) {
	var dm DialogManager
	dm.FlashError("Stop failed", time.Hour)
	if !dm.FlashIsError() {
		t.Fatal("expected FlashError to mark the toast as a failure")
	}
	dm.Flash("Started run", time.Hour)
	if dm.FlashIsError() {
		t.Fatal("expected a plain Flash to clear the failure flag")
	}
}

func TestDialogManager_FlashUndo(t *testing.T) {
	var dm DialogManager
	fired := false
	undo := func() tea.Msg { fired = true; return nil }

	dm.FlashUndo("Deleted run", undo, time.Hour)

	if _, ok := dm.FlashActive(); !ok {
		t.Fatal("expected the undo toast to be active")
	}
	cmd := dm.TakeUndo()
	if cmd == nil {
		t.Fatal("expected TakeUndo to return the armed command")
	}
	_ = cmd() // fire it
	if !fired {
		t.Fatal("expected the undo command to run")
	}
	if dm.TakeUndo() != nil {
		t.Fatal("undo must fire at most once")
	}
}

func TestDialogManager_FlashClearsPendingUndo(t *testing.T) {
	var dm DialogManager
	dm.FlashUndo("undoable", func() tea.Msg { return nil }, time.Hour)
	dm.Flash("plain message", time.Hour)
	if dm.TakeUndo() != nil {
		t.Fatal("a plain Flash must clear any pending undo")
	}
}

func TestDialogManager_TakeUndo_ExpiredReturnsNil(t *testing.T) {
	var dm DialogManager
	dm.FlashUndo("undoable", func() tea.Msg { return nil }, time.Hour)
	dm.flashExpiry = time.Now().Add(-time.Second) // expire it
	if dm.TakeUndo() != nil {
		t.Fatal("an expired toast must not return an undo")
	}
}

func TestDialogManager_ClearFlashIfExpired(t *testing.T) {
	var dm DialogManager
	dm.flashMessage = "test"
	dm.flashExpiry = time.Now().Add(-1 * time.Second)

	dm.ClearFlashIfExpired()

	if dm.flashMessage != "" {
		t.Fatal("expected flash message cleared")
	}
}

func TestDialogManager_SyncMouseState(t *testing.T) {
	var dm DialogManager

	// No dialog -> no command.
	cmd := dm.SyncMouseState()
	if cmd != nil {
		t.Fatal("expected nil cmd when no dialog and mouse not disabled")
	}

	// Show copy dialog -> should disable mouse.
	dm.Show(dlgCopy, NewCopyDialog("Test", "val"))
	cmd = dm.SyncMouseState()
	if cmd == nil {
		t.Fatal("expected non-nil cmd to disable mouse")
	}
	if !dm.mouseDisabled {
		t.Fatal("expected mouseDisabled=true")
	}

	// Already disabled -> no cmd.
	cmd = dm.SyncMouseState()
	if cmd != nil {
		t.Fatal("expected nil cmd when already disabled")
	}

	// Dismiss dialog -> should re-enable mouse.
	dm.Dismiss(dlgCopy)
	cmd = dm.SyncMouseState()
	if cmd == nil {
		t.Fatal("expected non-nil cmd to re-enable mouse")
	}
	if dm.mouseDisabled {
		t.Fatal("expected mouseDisabled=false")
	}
}

func TestDialogManager_SyncMouseState_Hold(t *testing.T) {
	var dm DialogManager

	// Hold alone disables the mouse even with no dialog.
	dm.SetMouseHold(true)
	cmd := dm.SyncMouseState()
	if cmd == nil {
		t.Fatal("expected disable-mouse cmd when hold is set")
	}
	if !dm.mouseDisabled {
		t.Fatal("expected mouseDisabled=true under hold")
	}

	// Releasing the hold re-enables the mouse.
	dm.SetMouseHold(false)
	cmd = dm.SyncMouseState()
	if cmd == nil {
		t.Fatal("expected enable-mouse cmd when hold is released")
	}
	if dm.mouseDisabled {
		t.Fatal("expected mouseDisabled=false after releasing hold")
	}

	// Copy dialog + hold together: still disabled; releasing hold while copy
	// dialog is open must keep mouse disabled.
	dm.SetMouseHold(true)
	_ = dm.SyncMouseState()
	dm.Show(dlgCopy, NewCopyDialog("Test", "val"))
	dm.SetMouseHold(false)
	cmd = dm.SyncMouseState()
	if cmd != nil {
		t.Fatal("expected no cmd: copy dialog still holds mouse disabled")
	}
	if !dm.mouseDisabled {
		t.Fatal("expected mouseDisabled to remain true while copy dialog open")
	}
}

func TestDialogManager_RenderOverlays_NoDialog(t *testing.T) {
	var dm DialogManager
	base := "hello world"
	out := dm.RenderOverlays(base, 80, 24)
	if out != base {
		t.Fatalf("expected base passthrough, got %q", out)
	}
}

// TestDialogManager_IsShuttingDown verifies the predicate before and after StartShutdown.
func TestDialogManager_IsShuttingDown(t *testing.T) {
	var dm DialogManager

	// No dialog → not shutting down.
	if dm.IsShuttingDown() {
		t.Fatal("expected IsShuttingDown=false with no dialog")
	}

	// Dialog present but not in shutdown mode.
	d := NewConfirmDialog("Quit?", "Are you sure?", func() tea.Msg { return nil })
	dm.Show(dlgConfirm, d)
	if dm.IsShuttingDown() {
		t.Fatal("expected IsShuttingDown=false before StartShutdown")
	}

	// Transition to shutdown.
	cmd := dm.StartShutdown()
	if !dm.IsShuttingDown() {
		t.Fatal("expected IsShuttingDown=true after StartShutdown")
	}
	if cmd == nil {
		t.Fatal("expected non-nil spinner command from StartShutdown")
	}
}

// TestDialogManager_StartShutdown_NoDialog returns nil when there is no confirm dialog.
func TestDialogManager_StartShutdown_NoDialog(t *testing.T) {
	var dm DialogManager
	cmd := dm.StartShutdown()
	if cmd != nil {
		t.Fatal("expected nil cmd when no confirm dialog")
	}
}

// TestDialogManager_UpdateSpinner_NoDialog returns nil without panicking.
func TestDialogManager_UpdateSpinner_NoDialog(t *testing.T) {
	var dm DialogManager
	cmd := dm.UpdateSpinner(nil)
	if cmd != nil {
		t.Fatal("expected nil cmd when no confirm dialog")
	}
}

// TestDialogManager_UpdateSpinner_WithDialog forwards a tick to the spinner.
func TestDialogManager_UpdateSpinner_WithDialog(t *testing.T) {
	var dm DialogManager
	d := NewConfirmDialog("Quit?", "Stopping...", func() tea.Msg { return nil })
	dm.Show(dlgConfirm, d)
	dm.StartShutdown() //nolint:errcheck

	// Produce a real spinner tick message via wrapSpinnerCmd.
	tickCmd := dm.confirm().spinnerTick()
	tickMsg := tickCmd()

	// Forward to UpdateSpinner; must not panic.
	_ = dm.UpdateSpinner(tickMsg)
}

func TestHelpDialog_ClosesOnCloseKeys(t *testing.T) {
	for _, key := range []string{"?", "esc", "enter", "q"} {
		msg := tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
		if key == "esc" {
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		}
		if key == "enter" {
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		}
		if !closes(&HelpDialog{}, msg) {
			t.Fatalf("expected %q to close the help dialog", key)
		}
	}
}

func TestHelpDialog_IgnoresOtherKeys(t *testing.T) {
	if closes(&HelpDialog{}, tea.KeyPressMsg{Code: 'x', Text: "x"}) {
		t.Fatal("expected 'x' to keep the help dialog open")
	}
}

func TestDialogManager_RenderOverlays_ConfirmTakesPrecedenceOverHelp(t *testing.T) {
	var dm DialogManager
	dm.Show(dlgHelp, &HelpDialog{})
	dialog := NewConfirmDialog("Quit", "Sure?", func() tea.Msg { return nil })
	dm.Show(dlgConfirm, dialog)

	out := dm.RenderOverlays("base", 80, 40)
	if out == "base" {
		t.Fatal("expected an overlay, got base output")
	}
	// The confirm dialog renders its title; the help overlay would render
	// "Keyboard Shortcuts" instead.
	if !strings.Contains(out, "Quit") || strings.Contains(out, "Keyboard Shortcuts") {
		t.Fatal("expected confirm dialog to take precedence over help overlay")
	}
}

func TestHelpDialog_ViewListsSections(t *testing.T) {
	d := &HelpDialog{}
	// A tall screen fits the whole reference table without scrolling.
	out := d.View(100, 80)
	for _, want := range []string{"Keyboard Shortcuts", "Global", "Navigate", "Exec view", "Notifications"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected help view to contain %q", want)
		}
	}
}

func TestHelpDialog_ScrollsWhenTallerThanScreen(t *testing.T) {
	d := &HelpDialog{}

	// A short screen can't fit every section; the last one is below the fold.
	top := d.View(100, 24)
	if !strings.Contains(top, "Global") {
		t.Fatal("expected the first section visible at the top")
	}
	if strings.Contains(top, "Notifications") {
		t.Fatal("expected the last section to be scrolled off on a short screen")
	}
	if !strings.Contains(top, "↑/↓ scroll") {
		t.Fatal("expected a scroll hint when content overflows")
	}

	// Jump to the end and the last section comes into view.
	if closes(d, tea.KeyPressMsg{Code: 'G', Text: "G"}) {
		t.Fatal("end key should scroll, not close")
	}
	bottom := d.View(100, 24)
	if !strings.Contains(bottom, "Notifications") {
		t.Fatal("expected the last section visible after scrolling to the end")
	}
}

func TestHelpDialog_ScrollKeysKeepOpen(t *testing.T) {
	d := &HelpDialog{}
	d.View(100, 24) // prime the viewport/total cache
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown},
		{Code: tea.KeyUp},
		{Code: tea.KeyPgDown},
		{Code: tea.KeyPgUp},
		{Code: tea.KeyHome},
		{Code: tea.KeyEnd},
	} {
		if closes(d, key) {
			t.Fatalf("scroll key %v should not close the help dialog", key)
		}
	}
}
