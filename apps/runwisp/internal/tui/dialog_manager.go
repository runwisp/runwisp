// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

// DialogManager owns dialog lifecycle, flash messages, and mouse-state sync.
// Extracted from Model to isolate modal overlay concerns.
type DialogManager struct {
	confirmDialog *ConfirmDialog
	copyDialog    *CopyDialog

	flashMessage string
	flashExpiry  time.Time

	// mouseDisabled tracks whether terminal mouse tracking has been
	// temporarily disabled (so users can natively select text).
	mouseDisabled bool
}

// HasConfirm reports whether a confirm dialog is active.
func (dm *DialogManager) HasConfirm() bool {
	return dm.confirmDialog != nil
}

// HasCopy reports whether a copy dialog is active.
func (dm *DialogManager) HasCopy() bool {
	return dm.copyDialog != nil
}

// ShowConfirm activates a confirm dialog.
func (dm *DialogManager) ShowConfirm(d ConfirmDialog) {
	dm.confirmDialog = &d
}

func (dm *DialogManager) DismissConfirm() {
	dm.confirmDialog = nil
}

// IsShuttingDown reports whether the confirm dialog is in the shutting-down state.
func (dm *DialogManager) IsShuttingDown() bool {
	return dm.confirmDialog != nil && dm.confirmDialog.shuttingDown
}

// StartShutdown transitions the active confirm dialog into the spinner state.
func (dm *DialogManager) StartShutdown() tea.Cmd {
	if dm.confirmDialog == nil {
		return nil
	}
	return dm.confirmDialog.StartShutdown()
}

// UpdateSpinner forwards a spinner tick to the active confirm dialog.
func (dm *DialogManager) UpdateSpinner(innerMsg tea.Msg) tea.Cmd {
	if dm.confirmDialog == nil {
		return nil
	}
	return dm.confirmDialog.UpdateSpinner(innerMsg)
}

// ShowCopy activates a copy dialog.
func (dm *DialogManager) ShowCopy(title, value string) {
	d := NewCopyDialog(title, value)
	dm.copyDialog = &d
}

func (dm *DialogManager) DismissCopy() {
	dm.copyDialog = nil
}

// UpdateConfirm dispatches input to the active confirm dialog.
// Returns a command and whether the dialog closed.
func (dm *DialogManager) UpdateConfirm(msg tea.Msg) (tea.Cmd, bool) {
	cmd, closed := dm.confirmDialog.Update(msg)
	if closed {
		dm.confirmDialog = nil
	}
	return cmd, closed
}

// UpdateConfirmKeep dispatches input to the active confirm dialog but does NOT
// dismiss it when closed. The caller is responsible for calling DismissConfirm
// after inspecting the returned command.
func (dm *DialogManager) UpdateConfirmKeep(msg tea.Msg) (tea.Cmd, bool) {
	return dm.confirmDialog.Update(msg)
}

// UpdateCopy dispatches input to the active copy dialog.
// Returns true when the dialog should close.
func (dm *DialogManager) UpdateCopy(msg tea.Msg) bool {
	if dm.copyDialog.Update(msg) {
		dm.copyDialog = nil
		return true
	}
	return false
}

// CopyToClipboard attempts clipboard copy. On failure, opens a CopyDialog
// so the user can manually select the text.
func (dm *DialogManager) CopyToClipboard(value string) tea.Cmd {
	err := clipboard.WriteAll(value)
	if err == nil {
		dm.flashMessage = "✓ Copied!"
		dm.flashExpiry = time.Now().Add(2 * time.Second)
		return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return flashExpiredMsg{}
		})
	}
	dm.ShowCopy("Copy", value)
	return dm.SyncMouseState()
}

// Flash sets a transient message shown in the help bar.
func (dm *DialogManager) Flash(msg string, duration time.Duration) tea.Cmd {
	dm.flashMessage = msg
	dm.flashExpiry = time.Now().Add(duration)
	return tea.Tick(duration, func(time.Time) tea.Msg {
		return flashExpiredMsg{}
	})
}

// ClearFlashIfExpired clears the flash message if past its expiry.
func (dm *DialogManager) ClearFlashIfExpired() {
	if time.Now().After(dm.flashExpiry) {
		dm.flashMessage = ""
	}
}

// FlashActive returns the flash message and whether it should be displayed.
func (dm *DialogManager) FlashActive() (string, bool) {
	if dm.flashMessage != "" && time.Now().Before(dm.flashExpiry) {
		return dm.flashMessage, true
	}
	return "", false
}

// SyncMouseState disables terminal mouse tracking when the copy dialog is
// visible and re-enables it when the dialog closes.
func (dm *DialogManager) SyncMouseState() tea.Cmd {
	wantDisabled := dm.copyDialog != nil

	if wantDisabled && !dm.mouseDisabled {
		dm.mouseDisabled = true
		return func() tea.Msg { return tea.DisableMouse() }
	}
	if !wantDisabled && dm.mouseDisabled {
		dm.mouseDisabled = false
		return func() tea.Msg { return tea.EnableMouseAllMotion() }
	}
	return nil
}

// RenderOverlays renders confirm/copy dialogs on top of the base output if active.
func (dm *DialogManager) RenderOverlays(base string, width, height int) string {
	if dm.confirmDialog != nil {
		return dm.confirmDialog.View(width, height)
	}
	if dm.copyDialog != nil {
		return dm.copyDialog.View(width, height)
	}
	return base
}
