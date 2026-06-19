// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
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
// so the two paths can never drift (a missing ActionDelete case here
// previously made the Delete button unclickable with the mouse).
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
	case execlist.ActionDelete:
		return confirmActionDelete, true
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
	m.dialogs.ShowConfirm(NewConfirmDialog(title, message, onConfirm))
	return nil
}

func (m *Model) confirmAction(action confirmAction) tea.Cmd {
	if m.client == nil {
		return nil
	}
	switch action {
	case confirmActionTrigger:
		return m.confirmTrigger()
	case confirmActionRestartService:
		return m.confirmRestartService()
	case confirmActionStopService:
		return m.confirmStopService()
	case confirmActionStop:
		return m.confirmStop()
	case confirmActionRetry:
		return m.confirmRetry()
	case confirmActionDelete:
		return m.confirmDelete()
	}
	return nil
}

func (m *Model) confirmTrigger() tea.Cmd {
	taskName := m.resolveTaskName()
	if taskName == "" {
		return nil
	}
	if m.isService(taskName) {
		return m.confirmAction(confirmActionRestartService)
	}
	client := m.client
	if task := m.taskDisplayByName(taskName); task != nil && len(task.Parameters) > 0 {
		m.dialogs.ShowParamForm(NewParamFormDialog(taskName, task.Parameters, func(params map[string]*string) tea.Cmd {
			return func() tea.Msg {
				run, err := client.TriggerRun(taskName, params)
				return uikit.TriggerRunMsg{TaskName: taskName, Run: run, Err: err}
			}
		}))
		return nil
	}
	return m.showConfirmDialog(
		"Run Now",
		fmt.Sprintf("Trigger a new run of\n'%s'?", taskName),
		func() tea.Msg {
			run, err := client.TriggerRun(taskName, nil)
			return uikit.TriggerRunMsg{TaskName: taskName, Run: run, Err: err}
		},
	)
}

func (m *Model) confirmRestartService() tea.Cmd {
	taskName := m.resolveTaskName()
	if taskName == "" {
		return nil
	}
	client := m.client
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
		func() tea.Msg {
			err := client.RestartService(taskName)
			return uikit.RestartServiceMsg{TaskName: taskName, Err: err}
		},
	)
}

func (m *Model) confirmStopService() tea.Cmd {
	taskName := m.resolveTaskName()
	if taskName == "" {
		return nil
	}
	client := m.client
	return m.showConfirmDialog(
		"Stop Service",
		fmt.Sprintf("Stop service\n'%s'?\nThe daemon will not restart it until you click Restart\nor the daemon itself restarts.", taskName),
		func() tea.Msg {
			err := client.StopService(taskName)
			return uikit.StopServiceMsg{TaskName: taskName, Err: err}
		},
	)
}

func (m *Model) confirmStop() tea.Cmd {
	run := m.currentRun()
	if run == nil || run.Status != model.PhaseRunning {
		return nil
	}
	client := m.client
	runID := run.ID
	taskName := run.TaskName
	return m.showConfirmDialog(
		"Stop Run",
		fmt.Sprintf("Stop the running execution of\n'%s'?", taskName),
		func() tea.Msg {
			err := client.StopRun(taskName, runID)
			return uikit.StopRunMsg{RunID: runID, TaskName: taskName, Err: err}
		},
	)
}

func (m *Model) confirmDelete() tea.Cmd {
	run := m.currentRun()
	if run == nil || m.execView == nil || !m.execView.CanDelete() {
		return nil
	}
	client := m.client
	runID := run.ID
	taskName := run.TaskName
	return m.showConfirmDialog(
		"Delete Run",
		fmt.Sprintf("Delete this run of\n'%s'?\nThe captured log is also removed.", taskName),
		func() tea.Msg {
			err := client.DeleteRun(taskName, runID)
			return uikit.DeleteRunMsg{RunID: runID, TaskName: taskName, Err: err}
		},
	)
}

func (m *Model) confirmRetry() tea.Cmd {
	run := m.currentRun()
	if run == nil || !run.IsRetryable() {
		return nil
	}
	client := m.client
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
		func() tea.Msg {
			newRun, err := client.TriggerRun(taskName, params)
			return uikit.TriggerRunMsg{TaskName: taskName, Run: newRun, Err: err, Retry: true}
		},
	)
}
