// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// DialogManager owns dialog lifecycle, flash messages, and mouse-state sync.
// Extracted from Model to isolate modal overlay concerns.
type DialogManager struct {
	confirmDialog *ConfirmDialog
	copyDialog    *CopyDialog
	helpDialog    *HelpDialog
	taskDetail    *TaskDetailDialog
	runDetail     *RunDetailDialog
	paramForm     *ParamFormDialog
	runParams     *RunParamsDialog
	logHistory    *LogHistoryDialog

	flashMessage string
	flashExpiry  time.Time
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

// HasParamForm reports whether a parameter form dialog is active.
func (dm *DialogManager) HasParamForm() bool {
	return dm.paramForm != nil
}

// ShowParamForm activates a parameter form dialog.
func (dm *DialogManager) ShowParamForm(d ParamFormDialog) {
	dm.paramForm = &d
}

func (dm *DialogManager) DismissParamForm() {
	dm.paramForm = nil
}

// UpdateParamForm dispatches input to the active param form. Returns the submit
// command (when confirmed) and whether the dialog closed.
func (dm *DialogManager) UpdateParamForm(msg tea.Msg) (tea.Cmd, bool) {
	cmd, closed := dm.paramForm.Update(msg)
	if closed {
		dm.paramForm = nil
	}
	return cmd, closed
}

// HasRunParams reports whether the read-only run-params dialog is active.
func (dm *DialogManager) HasRunParams() bool {
	return dm.runParams != nil
}

// ShowRunParams activates the read-only run-params dialog.
func (dm *DialogManager) ShowRunParams(d RunParamsDialog) {
	dm.runParams = &d
}

func (dm *DialogManager) DismissRunParams() {
	dm.runParams = nil
}

// UpdateRunParams dispatches input to the active run-params dialog.
// Returns true when the dialog closed.
func (dm *DialogManager) UpdateRunParams(msg tea.Msg) bool {
	if dm.runParams.Update(msg) {
		dm.runParams = nil
		return true
	}
	return false
}

// ShowCopy activates a copy dialog.
func (dm *DialogManager) ShowCopy(title, value string) {
	d := NewCopyDialog(title, value)
	dm.copyDialog = &d
}

func (dm *DialogManager) DismissCopy() {
	dm.copyDialog = nil
}

// HasHelp reports whether the help overlay is active.
func (dm *DialogManager) HasHelp() bool {
	return dm.helpDialog != nil
}

// ShowHelp activates the keyboard-shortcut overlay.
func (dm *DialogManager) ShowHelp() {
	d := NewHelpDialog()
	dm.helpDialog = &d
}

func (dm *DialogManager) DismissHelp() {
	dm.helpDialog = nil
}

// UpdateHelp dispatches input to the active help dialog.
// Returns true when the dialog closed.
func (dm *DialogManager) UpdateHelp(msg tea.Msg) bool {
	if dm.helpDialog.Update(msg) {
		dm.helpDialog = nil
		return true
	}
	return false
}

// HasTaskDetail reports whether the on-demand task inspector is active.
func (dm *DialogManager) HasTaskDetail() bool {
	return dm.taskDetail != nil
}

// ShowTaskDetail opens the task inspector for the named task. Health figures
// arrive asynchronously and are applied via ApplyTaskSummary.
func (dm *DialogManager) ShowTaskDetail(taskName string, task *model.TaskBrief) {
	d := NewTaskDetailDialog(taskName, task)
	dm.taskDetail = &d
}

func (dm *DialogManager) DismissTaskDetail() {
	dm.taskDetail = nil
}

// UpdateTaskDetail dispatches input to the active task inspector.
// Returns true when the dialog closed.
func (dm *DialogManager) UpdateTaskDetail(msg tea.Msg) bool {
	if dm.taskDetail.Update(msg) {
		dm.taskDetail = nil
		return true
	}
	return false
}

// ApplyTaskSummary feeds async health figures to the open inspector. A no-op
// when the inspector has since closed or the message is for another task.
func (dm *DialogManager) ApplyTaskSummary(msg uikit.TaskSummaryMsg) {
	if dm.taskDetail == nil {
		return
	}
	dm.taskDetail.ApplySummary(msg)
}

// HasRunDetail reports whether the on-demand run inspector is active.
func (dm *DialogManager) HasRunDetail() bool {
	return dm.runDetail != nil
}

// ShowRunDetail opens the run inspector for the given run.
func (dm *DialogManager) ShowRunDetail(run *model.Run, isService bool, instanceCount int) {
	d := NewRunDetailDialog(run, isService, instanceCount)
	dm.runDetail = &d
}

func (dm *DialogManager) DismissRunDetail() {
	dm.runDetail = nil
}

// RunDetailParent returns the parent-run reference of the open run inspector
// (set only when the run is a retry), so the interceptor can open it on enter.
func (dm *DialogManager) RunDetailParent() (taskName, runID string, ok bool) {
	if dm.runDetail == nil {
		return "", "", false
	}
	return dm.runDetail.ParentRef()
}

// UpdateRunDetail dispatches input to the active run inspector.
// Returns true when the dialog closed.
func (dm *DialogManager) UpdateRunDetail(msg tea.Msg) bool {
	if dm.runDetail.Update(msg) {
		dm.runDetail = nil
		return true
	}
	return false
}

// HasLogHistory reports whether the frame-history viewer is active.
func (dm *DialogManager) HasLogHistory() bool {
	return dm.logHistory != nil
}

// ShowLogHistory activates the frame-history viewer.
func (dm *DialogManager) ShowLogHistory(d LogHistoryDialog) {
	dm.logHistory = &d
}

func (dm *DialogManager) DismissLogHistory() {
	dm.logHistory = nil
}

// UpdateLogHistory dispatches input to the active frame-history viewer.
// Returns true when the dialog closed.
func (dm *DialogManager) UpdateLogHistory(msg tea.Msg) bool {
	if dm.logHistory.Update(msg) {
		dm.logHistory = nil
		return true
	}
	return false
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

// clipboardWriteAll is the seam tests use to deterministically force the
// fallback path. Production code points to atotto/clipboard.WriteAll.
var clipboardWriteAll = clipboard.WriteAll

// CopyToClipboard attempts clipboard copy. On failure, opens a CopyDialog
// so the user can manually select the text.
func (dm *DialogManager) CopyToClipboard(value string) tea.Cmd {
	err := clipboardWriteAll(value)
	if err == nil {
		dm.flashMessage = "✓ Copied!"
		dm.flashExpiry = time.Now().Add(2 * time.Second)
		return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return uikit.FlashExpiredMsg{}
		})
	}
	dm.ShowCopy("Copy", value)
	return dm.SyncMouseState()
}

// Flash sets a transient message shown in the help bar. It clears any pending
// undo so a stale inverse can't fire after an unrelated message.
func (dm *DialogManager) Flash(msg string, duration time.Duration) tea.Cmd {
	dm.flashMessage = msg
	dm.flashExpiry = time.Now().Add(duration)
	dm.undoCmd = nil
	return tea.Tick(duration, func(time.Time) tea.Msg {
		return uikit.FlashExpiredMsg{}
	})
}

// FlashUndo shows a toast that offers an inverse action. The undo command is
// fired by TakeUndo (the `u` key) and auto-expires with the toast.
func (dm *DialogManager) FlashUndo(msg string, undo tea.Cmd, duration time.Duration) tea.Cmd {
	dm.flashMessage = msg
	dm.flashExpiry = time.Now().Add(duration)
	dm.undoCmd = undo
	return tea.Tick(duration, func(time.Time) tea.Msg {
		return uikit.FlashExpiredMsg{}
	})
}

// TakeUndo returns the pending undo command (if the toast is still live) and
// clears it so it fires at most once. Returns nil when nothing is undoable.
func (dm *DialogManager) TakeUndo() tea.Cmd {
	if dm.undoCmd == nil || time.Now().After(dm.flashExpiry) {
		return nil
	}
	cmd := dm.undoCmd
	dm.undoCmd = nil
	dm.flashMessage = ""
	return cmd
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

// SyncMouseState disables terminal mouse tracking when the copy dialog is
// visible (or any mouse-hold is active) and re-enables it otherwise.
func (dm *DialogManager) SyncMouseState() tea.Cmd {
	wantDisabled := dm.copyDialog != nil || dm.runParams != nil || dm.mouseHold

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

// RenderOverlays renders confirm/copy/help dialogs on top of the base output
// if active. Confirm and copy take precedence over help.
func (dm *DialogManager) RenderOverlays(base string, width, height int) string {
	if dm.confirmDialog != nil {
		return dm.confirmDialog.View(width, height)
	}
	if dm.paramForm != nil {
		return dm.paramForm.View(width, height)
	}
	if dm.runParams != nil {
		return dm.runParams.View(width, height)
	}
	if dm.copyDialog != nil {
		return dm.copyDialog.View(width, height)
	}
	if dm.logHistory != nil {
		return dm.logHistory.View(width, height)
	}
	if dm.taskDetail != nil {
		return dm.taskDetail.View(width, height)
	}
	if dm.runDetail != nil {
		return dm.runDetail.View(width, height)
	}
	if dm.helpDialog != nil {
		return dm.helpDialog.View(width, height)
	}
	return base
}
