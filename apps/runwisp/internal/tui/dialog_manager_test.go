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

func TestDialogManager_ConfirmLifecycle(t *testing.T) {
	var dm DialogManager

	if dm.HasConfirm() {
		t.Fatal("expected no confirm dialog initially")
	}

	dialog := NewConfirmDialog("Test", "Are you sure?", func() tea.Msg { return nil })
	dm.ShowConfirm(dialog)

	if !dm.HasConfirm() {
		t.Fatal("expected confirm dialog after ShowConfirm")
	}

	dm.DismissConfirm()

	if dm.HasConfirm() {
		t.Fatal("expected no confirm dialog after DismissConfirm")
	}
}

func TestDialogManager_CopyLifecycle(t *testing.T) {
	var dm DialogManager

	if dm.HasCopy() {
		t.Fatal("expected no copy dialog initially")
	}

	dm.ShowCopy("Title", "some-value")

	if !dm.HasCopy() {
		t.Fatal("expected copy dialog after ShowCopy")
	}

	dm.DismissCopy()

	if dm.HasCopy() {
		t.Fatal("expected no copy dialog after DismissCopy")
	}
}

func TestDialogManager_RunParamsLifecycle(t *testing.T) {
	var dm DialogManager

	if dm.HasRunParams() {
		t.Fatal("expected no run-params dialog initially")
	}

	dm.ShowRunParams(NewRunParamsDialog("task", map[string]string{"k": "v"}))
	if !dm.HasRunParams() {
		t.Fatal("expected run-params dialog after ShowRunParams")
	}

	if !dm.UpdateRunParams(tea.KeyPressMsg{Code: tea.KeyEscape}) {
		t.Fatal("expected run-params dialog to close on Esc")
	}
	if dm.HasRunParams() {
		t.Fatal("expected run-params dialog to be nil after close")
	}
}

func TestDialogManager_UpdateConfirm_Closes(t *testing.T) {
	var dm DialogManager
	dialog := NewConfirmDialog("Test", "Confirm?", func() tea.Msg { return nil })
	dm.ShowConfirm(dialog)

	// Pressing Esc should close the dialog. Production dismisses via
	// UpdateConfirmKeep + DismissConfirm.
	_, closed := dm.UpdateConfirmKeep(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !closed {
		t.Fatal("expected confirm dialog to close on Esc")
	}
	dm.DismissConfirm()
	if dm.HasConfirm() {
		t.Fatal("expected confirm dialog to be nil after Esc")
	}
}

func TestDialogManager_UpdateCopy_Closes(t *testing.T) {
	var dm DialogManager
	dm.ShowCopy("Test", "value")

	closed := dm.UpdateCopy(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !closed {
		t.Fatal("expected copy dialog to close on Esc")
	}
	if dm.HasCopy() {
		t.Fatal("expected copy dialog to be nil after close")
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

func TestDialogManager_FlashUndo(t *testing.T) {
	var dm DialogManager
	fired := false
	undo := func() tea.Msg { fired = true; return nil }

	dm.FlashUndo("Deleted — press u to undo", undo, time.Hour)

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
	dm.ShowCopy("Test", "val")
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
	dm.DismissCopy()
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
	dm.ShowCopy("Test", "val")
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
	dm.ShowConfirm(d)
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
	dm.ShowConfirm(d)
	dm.StartShutdown() //nolint:errcheck

	// Produce a real spinner tick message via wrapSpinnerCmd.
	tickCmd := dm.confirmDialog.spinnerTick()
	tickMsg := tickCmd()

	// Forward to UpdateSpinner; must not panic.
	_ = dm.UpdateSpinner(tickMsg)
}

// TestDialogManager_UpdateConfirmKeep does not dismiss the dialog after a key press.
func TestDialogManager_UpdateConfirmKeep(t *testing.T) {
	var dm DialogManager
	d := NewConfirmDialog("Test", "Keep?", func() tea.Msg { return nil })
	dm.ShowConfirm(d)

	// Press Esc — normally closes; UpdateConfirmKeep must NOT dismiss it.
	_, closed := dm.UpdateConfirmKeep(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !closed {
		t.Fatal("expected closed=true from Esc")
	}
	// Dialog must still be present because UpdateConfirmKeep doesn't dismiss.
	if !dm.HasConfirm() {
		t.Fatal("expected dialog still present after UpdateConfirmKeep")
	}
}

// TestDialogManager_UpdateConfirmKeep_YesCmd returns the confirm cmd without dismissing.
func TestDialogManager_UpdateConfirmKeep_YesCmd(t *testing.T) {
	var dm DialogManager
	var fired bool
	d := NewConfirmDialog("Test", "Sure?", func() tea.Msg { fired = true; return nil })
	dm.ShowConfirm(d)

	// Drive the keyboard shortcut for "yes".
	cmd, closed := dm.UpdateConfirmKeep(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !closed {
		t.Fatal("expected closed=true on y")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd on y")
	}
	if !dm.HasConfirm() {
		t.Fatal("dialog must survive UpdateConfirmKeep")
	}
	cmd() //nolint:errcheck
	if !fired {
		t.Fatal("expected confirm callback to fire")
	}
}

func TestDialogManager_ParamFormLifecycle(t *testing.T) {
	var dm DialogManager
	if dm.HasParamForm() {
		t.Fatal("expected no param form initially")
	}

	params := []model.TaskParam{{Kind: model.ParamArg, Key: "branch"}}
	submit := func(map[string]*string) tea.Cmd { return nil }
	dm.ShowParamForm(NewParamFormDialog("deploy", params, submit))
	if !dm.HasParamForm() {
		t.Fatal("expected param form after ShowParamForm")
	}

	// Esc closes the form; UpdateParamForm reports closed and clears it.
	_, closed := dm.UpdateParamForm(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !closed {
		t.Fatal("expected param form to close on Esc")
	}
	if dm.HasParamForm() {
		t.Fatal("expected param form nil after close")
	}

	// DismissParamForm is also idempotent / safe to call directly.
	dm.ShowParamForm(NewParamFormDialog("deploy", params, submit))
	dm.DismissParamForm()
	if dm.HasParamForm() {
		t.Fatal("expected param form nil after DismissParamForm")
	}
}

func TestDialogManager_DismissRunParams(t *testing.T) {
	var dm DialogManager
	dm.ShowRunParams(NewRunParamsDialog("task", map[string]string{"k": "v"}))
	dm.DismissRunParams()
	if dm.HasRunParams() {
		t.Fatal("expected run-params nil after DismissRunParams")
	}
}

func TestDialogManager_TaskDetailLifecycle(t *testing.T) {
	var dm DialogManager
	if dm.HasTaskDetail() {
		t.Fatal("expected no task detail initially")
	}

	dm.ShowTaskDetail("alpha", &model.TaskBrief{Name: "alpha"})
	if !dm.HasTaskDetail() {
		t.Fatal("expected task detail after ShowTaskDetail")
	}

	// Async health figures flow in via ApplyTaskSummary while open (a no-op when
	// the message is for another task).
	dm.ApplyTaskSummary(uikit.TaskSummaryMsg{TaskName: "alpha", Total: 5, Success: 4, Failed: 1})

	// Esc closes the inspector.
	if !dm.UpdateTaskDetail(tea.KeyPressMsg{Code: tea.KeyEscape}) {
		t.Fatal("expected task detail to close on Esc")
	}
	if dm.HasTaskDetail() {
		t.Fatal("expected task detail nil after close")
	}

	// ApplyTaskSummary is a no-op once the inspector has closed.
	dm.ApplyTaskSummary(uikit.TaskSummaryMsg{TaskName: "alpha"})

	dm.ShowTaskDetail("beta", nil)
	dm.DismissTaskDetail()
	if dm.HasTaskDetail() {
		t.Fatal("expected task detail nil after DismissTaskDetail")
	}
}

func TestDialogManager_RunDetailLifecycle(t *testing.T) {
	var dm DialogManager
	if dm.HasRunDetail() {
		t.Fatal("expected no run detail initially")
	}

	dm.ShowRunDetail(&model.Run{ID: "r1", TaskName: "t1"}, false, 1)
	if !dm.HasRunDetail() {
		t.Fatal("expected run detail after ShowRunDetail")
	}

	if !dm.UpdateRunDetail(tea.KeyPressMsg{Code: tea.KeyEscape}) {
		t.Fatal("expected run detail to close on Esc")
	}
	if dm.HasRunDetail() {
		t.Fatal("expected run detail nil after close")
	}
}

func TestDialogManager_LogHistoryLifecycle(t *testing.T) {
	var dm DialogManager
	if dm.HasLogHistory() {
		t.Fatal("expected no log history initially")
	}

	dm.ShowLogHistory(NewLogHistoryDialog(0, [][]string{{"frame"}}, "committed"))
	if !dm.HasLogHistory() {
		t.Fatal("expected log history after ShowLogHistory")
	}

	if !dm.UpdateLogHistory(tea.KeyPressMsg{Code: tea.KeyEscape}) {
		t.Fatal("expected log history to close on Esc")
	}
	if dm.HasLogHistory() {
		t.Fatal("expected log history nil after close")
	}

	dm.ShowLogHistory(NewLogHistoryDialog(0, [][]string{{"x"}}, "y"))
	dm.DismissLogHistory()
	if dm.HasLogHistory() {
		t.Fatal("expected log history nil after DismissLogHistory")
	}
}

func TestDialogManager_HelpLifecycle(t *testing.T) {
	var dm DialogManager

	if dm.HasHelp() {
		t.Fatal("expected no help dialog initially")
	}

	dm.ShowHelp()

	if !dm.HasHelp() {
		t.Fatal("expected help dialog after ShowHelp")
	}

	dm.DismissHelp()

	if dm.HasHelp() {
		t.Fatal("expected no help dialog after DismissHelp")
	}
}

func TestDialogManager_UpdateHelp_ClosesOnCloseKeys(t *testing.T) {
	for _, key := range []string{"?", "esc", "enter", "q"} {
		var dm DialogManager
		dm.ShowHelp()

		msg := tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
		if key == "esc" {
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		}
		if key == "enter" {
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		}
		if !dm.UpdateHelp(msg) {
			t.Fatalf("expected %q to close the help dialog", key)
		}
		if dm.HasHelp() {
			t.Fatalf("expected help dialog dismissed after %q", key)
		}
	}
}

func TestDialogManager_UpdateHelp_IgnoresOtherKeys(t *testing.T) {
	var dm DialogManager
	dm.ShowHelp()

	if dm.UpdateHelp(tea.KeyPressMsg{Code: 'x', Text: "x"}) {
		t.Fatal("expected 'x' to keep the help dialog open")
	}
	if !dm.HasHelp() {
		t.Fatal("expected help dialog still active")
	}
}

func TestDialogManager_RenderOverlays_ConfirmTakesPrecedenceOverHelp(t *testing.T) {
	var dm DialogManager
	dm.ShowHelp()
	dialog := NewConfirmDialog("Quit", "Sure?", func() tea.Msg { return nil })
	dm.ShowConfirm(dialog)

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
	d := NewHelpDialog()
	// A tall screen fits the whole reference table without scrolling.
	out := d.View(100, 80)
	for _, want := range []string{"Keyboard Shortcuts", "Global", "Navigate", "Exec view", "Notifications"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected help view to contain %q", want)
		}
	}
}

func TestHelpDialog_ScrollsWhenTallerThanScreen(t *testing.T) {
	d := NewHelpDialog()

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
	if d.Update(tea.KeyPressMsg{Code: 'G', Text: "G"}) {
		t.Fatal("end key should scroll, not close")
	}
	bottom := d.View(100, 24)
	if !strings.Contains(bottom, "Notifications") {
		t.Fatal("expected the last section visible after scrolling to the end")
	}
}

func TestHelpDialog_ScrollKeysKeepOpen(t *testing.T) {
	d := NewHelpDialog()
	d.View(100, 24) // prime the viewport/total cache
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown},
		{Code: tea.KeyUp},
		{Code: tea.KeyPgDown},
		{Code: tea.KeyPgUp},
		{Code: tea.KeyHome},
		{Code: tea.KeyEnd},
	} {
		if d.Update(key) {
			t.Fatalf("scroll key %v should not close the help dialog", key)
		}
	}
}
