// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/execlist"
)

const doubleClickThreshold = 400 * time.Millisecond

type confirmAction int

const (
	confirmActionTrigger confirmAction = iota
	confirmActionStop
	confirmActionRetry
	confirmActionRestartService
	confirmActionStopService
	confirmActionDelete
)

// actionConfirm maps the exec view's header action button to its confirm
// constant. Single source of truth shared by mouse-click and Enter handling
// so the two paths can never drift. The Delete button is a separate header
// item (HeaderFocusDelete), not an Action.
func actionConfirm(a execlist.Action) (confirmAction, bool) {
	switch a {
	case execlist.ActionStop:
		return confirmActionStop, true
	case execlist.ActionStopService:
		return confirmActionStopService, true
	case execlist.ActionRetry:
		return confirmActionRetry, true
	case execlist.ActionRestartService:
		return confirmActionRestartService, true
	}
	return 0, false
}

func (m *Model) focusSidebar() tea.Cmd {
	m.panelFocus = uikit.PanelSidebar
	m.sidebar.SetFocused(true)
	m.execList.SetFocused(false)
	m.homeCursor = -1
	if m.execView != nil {
		m.execView.SetFocused(false)
	}
	return m.dialogs.SyncMouseState()
}

func (m *Model) focusMainPanel() tea.Cmd {
	m.panelFocus = uikit.PanelMain
	m.sidebar.SetFocused(false)
	m.homeCursor = -1
	m.execList.SetFocused(m.execView == nil && m.sidebar.ActivePage() == uikit.PageHome)
	if m.execView != nil {
		m.execView.SetFocused(true)
	}
	return m.dialogs.SyncMouseState()
}

func (m *Model) focusHomeField(index int) tea.Cmd {
	m.panelFocus = uikit.PanelMain
	m.sidebar.SetFocused(false)
	m.homeCursor = index
	m.execList.SetFocused(false)
	if m.execView != nil {
		m.execView.SetFocused(false)
	}
	return m.dialogs.SyncMouseState()
}

func (m *Model) applySidebarSelectionChange(prevPage uikit.Page, prevTask string) tea.Cmd {
	if m.sidebar.ActivePage() == prevPage && m.sidebar.ActiveTask() == prevTask {
		// Selection didn't move, but if a run's exec log is open, re-selecting the
		// task closes the log and returns to its detail view.
		if m.execView != nil {
			return m.closeExecView()
		}
		return nil
	}
	m.homeCursor = -1
	m.execList.SetFilter(m.sidebar.ActiveTask())
	m.recalcExecListHeight()

	cmds := []tea.Cmd{m.dialogs.SyncMouseState(), m.fetchExecWindow()}
	if m.execView != nil {
		if cmd := m.closeExecView(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if cmd := m.autoOpenService(m.sidebar.ActiveTask()); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if m.sidebar.ActivePage() == uikit.PageInfo && prevPage != uikit.PageInfo {
		cmds = append(cmds, m.streams.FetchMetricsHistory(), m.streams.FetchSystemStats(), m.streams.FetchRunSummary())
	}

	return tea.Batch(cmds...)
}

func (m *Model) mainHeaderHeight() int {
	base := m.layout.homeH
	if m.sidebar.ActiveTask() != "" {
		base = m.layout.taskH
	}
	if m.sidebar.ActivePage() == uikit.PageHome {
		base += m.notifications.PanelHeight()
	}
	return base
}

func (m *Model) detectDoubleClick(y int) bool {
	now := time.Now()
	isDoubleClick := y == m.mouse.lastClickY && now.Sub(m.mouse.lastClickTime) < doubleClickThreshold
	m.mouse.lastClickTime = now
	m.mouse.lastClickY = y
	return isDoubleClick
}

func (m *Model) currentRun() *model.Run {
	if m.execView == nil {
		return nil
	}
	return m.execView.Run
}

func (m *Model) showConfirmDialog(title, message string, onConfirm tea.Cmd) tea.Cmd {
	m.dialogs.Show(dlgConfirm, NewConfirmDialog(title, message, onConfirm))
	return nil
}

func (m *Model) confirmAction(action confirmAction) tea.Cmd {
	switch action {
	case confirmActionTrigger:
		return m.triggerRun()
	case confirmActionRestartService:
		return m.confirmRestartService()
	case confirmActionStopService:
		return m.confirmStopService()
	case confirmActionStop:
		return m.confirmStop()
	case confirmActionRetry:
		return m.retryRun()
	case confirmActionDelete:
		return m.deleteCurrentRun()
	}
	return nil
}

// triggerRun runs a task now, gated by a confirm dialog. Services confirm via
// the restart path (a restart kills running instances), and parameterized tasks
// open the param form — it gathers input and runs on submit, which is itself an
// explicit confirmation.
func (m *Model) triggerRun() tea.Cmd {
	taskName := m.resolveTaskName()
	if taskName == "" {
		return nil
	}
	if m.isService(taskName) {
		return m.confirmAction(confirmActionRestartService)
	}
	if task := m.taskDisplayByName(taskName); task != nil && len(task.Parameters) > 0 {
		streams := m.streams
		m.dialogs.Show(dlgParamForm, NewParamFormDialog(taskName, task.Parameters, func(params map[string]*string) tea.Cmd {
			return streams.TriggerRun(taskName, params, false)
		}))
		return nil
	}
	return m.showConfirmDialog(
		"Run Task",
		fmt.Sprintf("Run '%s' now?", taskName),
		m.streams.TriggerRun(taskName, nil, false),
	)
}

func (m *Model) confirmRestartService() tea.Cmd {
	taskName := m.resolveTaskName()
	if taskName == "" {
		return nil
	}
	if m.info.StoppedServices[taskName] {
		return m.showConfirmDialog(
			"Start Service",
			fmt.Sprintf("Start service\n'%s'?", taskName),
			m.streams.RestartService(taskName),
		)
	}
	instances := m.serviceInstances(taskName)
	var prompt string
	if instances > 1 {
		prompt = fmt.Sprintf("Cancel and restart all %d instances of\n'%s'?", instances, taskName)
	} else {
		prompt = fmt.Sprintf("Cancel and restart\n'%s'?", taskName)
	}
	return m.showConfirmDialog(
		"Restart Service",
		prompt,
		m.streams.RestartService(taskName),
	)
}

func (m *Model) confirmStopService() tea.Cmd {
	taskName := m.resolveTaskName()
	if taskName == "" {
		return nil
	}
	return m.showConfirmDialog(
		"Stop Service",
		fmt.Sprintf("Stop service\n'%s'?\nIt stays stopped until you start it\nagain or the daemon restarts.", taskName),
		m.streams.StopService(taskName),
	)
}

func (m *Model) confirmStop() tea.Cmd {
	run := m.currentRun()
	if run == nil || run.Status != model.PhaseRunning {
		return nil
	}
	return m.showConfirmDialog(
		"Stop Run",
		fmt.Sprintf("Stop the running execution of\n'%s'?", run.TaskName),
		m.streams.StopRun(run.ID, run.TaskName),
	)
}

// deleteCurrentRun soft-deletes the run shown in the exec view immediately. The
// delete is reversible (soft delete + restore), so the result toast offers undo
// instead of a confirm dialog. It goes through the same bulk/delete selector
// the Web UI uses for a single run (and this model's own bulk delete),
// rather than the single-run DELETE /api/runs/{runId} route, so both surfaces
// exercise one delete path.
func (m *Model) deleteCurrentRun() tea.Cmd {
	run := m.currentRun()
	if run == nil || m.execView == nil || !m.execView.CanDelete() {
		return nil
	}
	return m.streams.DeleteRun(run.ID, run.TaskName)
}

// retryRun re-runs the current execution, gated by a confirm dialog, reproducing
// its original params.
func (m *Model) retryRun() tea.Cmd {
	run := m.currentRun()
	if run == nil || !run.IsRetryable() {
		return nil
	}
	taskName := run.TaskName
	// Reproduce the original run exactly: present params carry forward, omitted
	// ones stay omitted (rather than picking their default back up on re-resolve).
	var params map[string]*string
	if task := m.taskDisplayByName(taskName); task != nil {
		params = model.SuppliedFromResolved(task.Parameters, run.Params)
	}
	return m.showConfirmDialog(
		"Retry Run",
		fmt.Sprintf("Retry '%s'?", taskName),
		m.streams.TriggerRun(taskName, params, true),
	)
}
