// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
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

	dm.Flash("Hello!", 100*time.Millisecond)

	msg, ok = dm.FlashActive()
	if !ok || msg != "Hello!" {
		t.Fatalf("expected flash 'Hello!', got ok=%v msg=%q", ok, msg)
	}

	// Wait for expiry.
	time.Sleep(150 * time.Millisecond)

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

func TestDialogManager_RenderOverlays_NoDialog(t *testing.T) {
	var dm DialogManager
	base := "hello world"
	out := dm.RenderOverlays(base, 80, 24)
	if out != base {
		t.Fatalf("expected base passthrough, got %q", out)
	}
}
