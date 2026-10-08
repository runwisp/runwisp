// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// dialog is a modal overlay. Update handles a key or mouse message and reports
// a command to run and whether the dialog should close.
type dialog interface {
	Update(msg tea.Msg) (tea.Cmd, bool)
	View(screenWidth, screenHeight int) string
}

// dialogKind names a dialog slot. The order is the precedence: when several
// dialogs are open, the lowest kind renders and gets first claim on input.
type dialogKind int

const (
	dlgConfirm dialogKind = iota
	dlgParamForm
	dlgRunParams
	dlgCopy
	dlgLogHistory
	dlgNewRelease
	dlgTaskDetail
	dlgRunDetail
	dlgHelp
	dialogKinds
)

// Flash durations: a quick acknowledgement, a passing status, an action's
// result, and an error or undo offer the operator needs time to read or act on.
const (
	flashBrief  = 2 * time.Second
	flashShort  = 3 * time.Second
	flashResult = 4 * time.Second
	flashLong   = 6 * time.Second
)

// DialogManager owns dialog lifecycle, flash messages, and mouse-state sync.
type DialogManager struct {
	open [dialogKinds]dialog

	flashMessage string
	flashExpiry  time.Time
	flashError   bool
	// undoCmd is the inverse action offered by the current toast, fired by `u`.
	// It rides with the flash so it auto-expires on the same clock; a plain
	// Flash clears it so a stale undo can never fire after an unrelated message.
	undoCmd tea.Cmd

	// mouseDisabled tracks whether terminal mouse tracking has been
	// temporarily disabled (so users can natively select text).
	mouseDisabled bool

	// mouseHold, when true, keeps mouse tracking disabled even if no dialog
	// is active. Set by callers (e.g. fullscreen log) that need native
	// terminal text selection while the TUI keeps running.
	mouseHold bool
}

// SetMouseHold marks whether an external caller (outside any dialog) needs
// the mouse kept disabled for native terminal text selection.
func (dm *DialogManager) SetMouseHold(hold bool) {
	dm.mouseHold = hold
}

// Has reports whether a dialog of the given kind is open.
func (dm *DialogManager) Has(k dialogKind) bool {
	return dm.open[k] != nil
}

// Show opens d in the given slot, replacing any dialog already there.
func (dm *DialogManager) Show(k dialogKind, d dialog) {
	dm.open[k] = d
}

// Dismiss closes the dialog in the given slot (a no-op when none is open).
func (dm *DialogManager) Dismiss(k dialogKind) {
	dm.open[k] = nil
}

// top returns the highest-precedence open dialog, or ok=false when none is.
func (dm *DialogManager) top() (dialogKind, dialog, bool) {
	for k, d := range dm.open {
		if d != nil {
			return dialogKind(k), d, true
		}
	}
	return 0, nil, false
}

func (dm *DialogManager) confirm() *ConfirmDialog {
	d, _ := dm.open[dlgConfirm].(*ConfirmDialog)
	return d
}

// IsShuttingDown reports whether the confirm dialog is in the shutting-down state.
func (dm *DialogManager) IsShuttingDown() bool {
	d := dm.confirm()
	return d != nil && d.shuttingDown
}

// StartShutdown transitions the active confirm dialog into the spinner state.
func (dm *DialogManager) StartShutdown() tea.Cmd {
	d := dm.confirm()
	if d == nil {
		return nil
	}
	return d.StartShutdown()
}

// UpdateSpinner forwards a spinner tick to the active confirm dialog.
func (dm *DialogManager) UpdateSpinner(innerMsg tea.Msg) tea.Cmd {
	d := dm.confirm()
	if d == nil {
		return nil
	}
	return d.UpdateSpinner(innerMsg)
}

// ApplyTaskSummary feeds async health figures to the open inspector. A no-op
// when the inspector has since closed or the message is for another task.
func (dm *DialogManager) ApplyTaskSummary(msg uikit.TaskSummaryMsg) {
	if d, ok := dm.open[dlgTaskDetail].(*TaskDetailDialog); ok {
		d.ApplySummary(msg)
	}
}

// clipboardWriteAll is the seam tests use to deterministically force the
// fallback path. Production code points to atotto/clipboard.WriteAll.
var clipboardWriteAll = clipboard.WriteAll

// CopyToClipboard attempts clipboard copy. On failure, opens a CopyDialog
// so the user can manually select the text.
func (dm *DialogManager) CopyToClipboard(value string) tea.Cmd {
	if err := clipboardWriteAll(value); err == nil {
		return dm.Flash("Copied", flashBrief)
	}
	dm.Show(dlgCopy, NewCopyDialog("Copy", value))
	return dm.SyncMouseState()
}

// Flash shows a transient success/info toast. It clears any pending undo so a
// stale inverse can't fire after an unrelated message.
func (dm *DialogManager) Flash(msg string, duration time.Duration) tea.Cmd {
	return dm.setFlash(msg, duration, nil, false)
}

// FlashError shows a transient toast styled as a failure.
func (dm *DialogManager) FlashError(msg string, duration time.Duration) tea.Cmd {
	return dm.setFlash(msg, duration, nil, true)
}

// FlashUndo shows a toast that offers an inverse action. The undo command is
// fired by TakeUndo (the `u` key) and auto-expires with the toast.
func (dm *DialogManager) FlashUndo(msg string, undo tea.Cmd, duration time.Duration) tea.Cmd {
	return dm.setFlash(msg, duration, undo, false)
}

func (dm *DialogManager) setFlash(msg string, duration time.Duration, undo tea.Cmd, isErr bool) tea.Cmd {
	dm.flashMessage = msg
	dm.flashExpiry = time.Now().Add(duration)
	dm.flashError = isErr
	dm.undoCmd = undo
	return tea.Tick(duration, func(time.Time) tea.Msg {
		return uikit.FlashExpiredMsg{}
	})
}

// TakeUndo returns the pending undo command (if the toast is still live) and
// clears it so it fires at most once. Returns nil when nothing is undoable.
func (dm *DialogManager) TakeUndo() tea.Cmd {
	if !dm.HasUndo() {
		return nil
	}
	cmd := dm.undoCmd
	dm.undoCmd = nil
	dm.flashMessage = ""
	return cmd
}

// HasUndo reports whether the live toast offers an undo.
func (dm *DialogManager) HasUndo() bool {
	return dm.undoCmd != nil && time.Now().Before(dm.flashExpiry)
}

// ClearFlashIfExpired clears the flash message (and any undo) if past expiry.
func (dm *DialogManager) ClearFlashIfExpired() {
	if time.Now().After(dm.flashExpiry) {
		dm.flashMessage = ""
		dm.undoCmd = nil
	}
}

// FlashActive returns the flash message and whether it should be displayed.
func (dm *DialogManager) FlashActive() (string, bool) {
	if dm.flashMessage != "" && time.Now().Before(dm.flashExpiry) {
		return dm.flashMessage, true
	}
	return "", false
}

// FlashIsError reports whether the current toast is a failure.
func (dm *DialogManager) FlashIsError() bool {
	return dm.flashError
}

// MouseDisabled reports whether terminal mouse tracking is currently
// disabled. View() reads this to set tea.View.MouseMode, since v2 dropped
// the imperative enable/disable mouse commands in favor of per-frame state.
func (dm *DialogManager) MouseDisabled() bool {
	return dm.mouseDisabled
}

// SyncMouseState disables terminal mouse tracking while a dialog that shows
// selectable text is open (copy, run params) or any mouse-hold is active,
// and re-enables it otherwise. The actual mode switch happens in View() via
// MouseDisabled; the non-nil return here only signals that a transition
// occurred, for callers that batch it alongside other commands.
func (dm *DialogManager) SyncMouseState() tea.Cmd {
	wantDisabled := dm.Has(dlgCopy) || dm.Has(dlgRunParams) || dm.mouseHold

	if wantDisabled == dm.mouseDisabled {
		return nil
	}
	dm.mouseDisabled = wantDisabled
	return func() tea.Msg { return nil }
}

// RenderOverlays renders the highest-precedence open dialog over the base
// output, or returns base unchanged when no dialog is open.
func (dm *DialogManager) RenderOverlays(base string, width, height int) string {
	if _, d, ok := dm.top(); ok {
		return d.View(width, height)
	}
	return base
}
