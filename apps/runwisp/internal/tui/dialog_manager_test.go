// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

func TestDialogManager_UpdateConfirm_Closes(t *testing.T) {
	var dm DialogManager
	dialog := NewConfirmDialog("Test", "Confirm?", func() tea.Msg { return nil })
	dm.ShowConfirm(dialog)

	// Pressing Esc should close the dialog.
	_, closed := dm.UpdateConfirm(tea.KeyMsg{Type: tea.KeyEscape})
	if !closed {
		t.Fatal("expected confirm dialog to close on Esc")
	}
	if dm.HasConfirm() {
		t.Fatal("expected confirm dialog to be nil after Esc")
	}
}

func TestDialogManager_UpdateCopy_Closes(t *testing.T) {
	var dm DialogManager
	dm.ShowCopy("Test", "value")

	closed := dm.UpdateCopy(tea.KeyMsg{Type: tea.KeyEscape})
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
	_, closed := dm.UpdateConfirmKeep(tea.KeyMsg{Type: tea.KeyEscape})
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
	cmd, closed := dm.UpdateConfirmKeep(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
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

		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		if key == "esc" {
			msg = tea.KeyMsg{Type: tea.KeyEscape}
		}
		if key == "enter" {
			msg = tea.KeyMsg{Type: tea.KeyEnter}
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

	if dm.UpdateHelp(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) {
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
	out := d.View(100, 50)
	for _, want := range []string{"Keyboard Shortcuts", "Global", "Navigate", "Exec view", "Notifications"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected help view to contain %q", want)
		}
	}
}
