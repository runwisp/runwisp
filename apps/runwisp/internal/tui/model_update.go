// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"encoding/json"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
)

// Update processes messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Confirmation dialog intercepts all input when visible.
	if m.dialogs.HasConfirm() {
		if m.dialogs.IsShuttingDown() {
			switch msg := msg.(type) {
			case tea.KeyMsg:
				if msg.String() == "ctrl+c" {
					m.streams.Shutdown()
					m.quitAction = QuitKeepDaemon
					return m, tea.Quit
				}
				return m, nil
			case spinnerTickMsg:
				cmd := m.dialogs.UpdateSpinner(msg.Inner)
				return m, cmd
			case shutdownDoneMsg:
				return m, tea.Quit
			case tea.MouseMsg:
				return m, nil
			}
		} else {
			switch msg := msg.(type) {
			case tea.KeyMsg:
				if msg.String() == "ctrl+c" {
					m.streams.Shutdown()
					m.quitAction = QuitKeepDaemon
					return m, tea.Quit
				}
				cmd, closed := m.dialogs.UpdateConfirmKeep(msg)
				if cmd != nil {
					return m, m.execConfirmCmd(cmd, closed)
				}
				if closed {
					m.dialogs.DismissConfirm()
				}
				return m, nil
			case tea.MouseMsg:
				cmd, closed := m.dialogs.UpdateConfirmKeep(msg)
				if cmd != nil {
					return m, m.execConfirmCmd(cmd, closed)
				}
				if closed {
					m.dialogs.DismissConfirm()
				}
				return m, nil
			}
		}
	}

	// Copy dialog intercepts all input when visible.
	if m.dialogs.HasCopy() {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.String() == "ctrl+c" {
				m.dialogs.DismissCopy()
				cmd := m.dialogs.SyncMouseState()
				m.showQuitConfirm()
				return m, cmd
			}
			if m.dialogs.UpdateCopy(msg) {
				return m, m.dialogs.SyncMouseState()
			}
			return m, nil
		case tea.MouseMsg:
			if m.dialogs.UpdateCopy(msg) {
				return m, m.dialogs.SyncMouseState()
			}
			return m, nil
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.updateLayout()
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.showQuitConfirm()
			return m, nil

		case "esc":
			if m.execView != nil {
				m.closeExecView()
				return m, nil
			}
			if m.panelFocus == PanelMain {
				return m, m.focusSidebar()
			}

		case "backspace":
			if m.execView != nil {
				m.closeExecView()
				return m, nil
			}

		case "left", "h":
			if m.execView == nil {
				if m.panelFocus == PanelMain && m.sidebar.ActivePage() == PageDebug && m.debugView.pane.hScroll > 0 {
					break
				}
				return m, m.focusSidebar()
			}

			ev := m.execView
			atEdge := ev.headerFocus == headerFocusBack || ev.headerFocus == headerFocusStarted ||
				(ev.headerFocus == headerFocusNone && ev.pane.hScroll <= 0)
			if atEdge {
				return m, m.focusSidebar()
			}

		case "right", "l":
			if m.execView == nil {
				if m.panelFocus == PanelMain && m.sidebar.ActivePage() == PageDebug {
					break
				}
				return m, m.focusMainPanel()
			}
			if m.panelFocus == PanelSidebar {
				return m, m.focusMainPanel()
			}

		case "enter":
			if m.execView != nil && m.execView.headerFocus != headerFocusNone {
				switch m.execView.headerFocus {
				case headerFocusBack:
					m.closeExecView()
					return m, nil
				case headerFocusAction:
					switch m.execView.Action() {
					case execViewActionStop:
						return m, m.confirmAction(confirmActionStop)
					case execViewActionRetry:
						return m, m.confirmAction(confirmActionRetry)
					}
				case headerFocusStarted, headerFocusDuration, headerFocusID:
					return m, m.copyExecField()
				}
				return m, nil
			}
			if m.panelFocus == PanelMain && m.execView == nil {
				if m.homeCursor >= 0 {
					return m, m.activateHomeField()
				}
				if run := m.execList.SelectedRun(); run != nil {
					return m, m.openExecView(run)
				}
			}

		case "r":
			if m.execView != nil {
				return m, m.confirmAction(confirmActionRetry)
			}
			return m, m.confirmAction(confirmActionTrigger)

		case "s":
			if m.execView != nil {
				return m, m.confirmAction(confirmActionStop)
			}

		case "up", "k":
			if m.panelFocus == PanelMain && m.execView == nil && m.sidebar.ActivePage() == PageHome && m.sidebar.ActiveTask() == "" {
				if m.execList.Cursor() == 0 || m.execList.totalCount() == 0 {
					fields := homeFields(m.info, m.hasLaunchTicket())
					if len(fields) > 0 {
						if m.homeCursor < 0 {
							return m, m.focusHomeField(len(fields) - 1)
						} else if m.homeCursor > 0 {
							m.homeCursor--
						}
						return m, m.dialogs.SyncMouseState()
					}
				}
			}

		case "down", "j":
			if m.panelFocus == PanelMain && m.execView == nil && m.homeCursor >= 0 {
				fields := homeFields(m.info, m.hasLaunchTicket())
				if m.homeCursor < len(fields)-1 {
					m.homeCursor++
				} else {
					m.homeCursor = -1
					m.execList.SetFocused(true)
				}
				return m, m.dialogs.SyncMouseState()
			}
		}

		// Delegate to focused sub-component.
		if m.execView != nil && m.panelFocus != PanelSidebar {
			cmd := m.execView.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else if m.panelFocus == PanelSidebar {
			prevPage := m.sidebar.ActivePage()
			prevTask := m.sidebar.ActiveTask()
			m.sidebar.Update(msg)
			if cmd := m.applySidebarSelectionChange(prevPage, prevTask); cmd != nil {
				cmds = append(cmds, cmd)
			}
		} else if m.panelFocus == PanelMain && m.sidebar.ActivePage() == PageInfo {
			m.infoView.Update(msg)
		} else if m.panelFocus == PanelMain && m.sidebar.ActivePage() == PageDebug {
			m.debugView.Update(msg)
		} else if m.panelFocus == PanelMain && m.homeCursor < 0 {
			cmd := m.execList.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			if m.execList.NeedsFetch() {
				cmds = append(cmds, m.fetchExecWindow())
			}
		}

	case execWindowFetchedMsg:
		m.execWindow.ApplyFetch(msg.Items, msg.Offset, msg.Total)

	case sseConnectedMsg:
		cmds = append(cmds, m.streams.OnSSEConnected(msg.ch))

	case sseEventMsg:
		cmds = append(cmds, m.handleSSEEvent(msg.Event))
		cmds = append(cmds, m.streams.ContinueListeningSSE())

	case sseDisconnectedMsg:
		m.debugView.AppendLine("Events stream disconnected. Reconnecting...")
		cmds = append(cmds, m.streams.SubscribeEvents())

	case logStreamConnectedMsg:
		if m.execView != nil && m.execView.RunID() == msg.RunID {
			cmds = append(cmds, m.streams.OnLogConnected(msg.RunID, msg.Ch))
		}

	case logChunkMsg:
		if m.execView != nil && m.execView.RunID() == msg.RunID {
			m.execView.AppendChunk(msg.Chunk)
			cmds = append(cmds, m.streams.ContinueListeningLog(msg.RunID))
		}

	case logDoneMsg:
		if m.execView != nil && m.execView.RunID() == msg.RunID {
			m.execView.FlushPending()
			if m.execView.run.Status == model.PhaseRunning {
				cmds = append(cmds, m.scheduleLogReconnect(msg.RunID))
			}
		}

	case logBatchMsg:
		if m.execView != nil && m.execView.RunID() == msg.RunID {
			for _, line := range msg.Lines {
				m.execView.AppendChunk(line + "\n")
			}
			m.execView.FlushPending()
			if m.execView.run.Status == model.PhaseRunning {
				cmds = append(cmds, m.scheduleLogReconnect(msg.RunID))
			}
		}

	case DebugLogMsg:
		m.debugView.AppendLine(msg.Message)

	case openBrowserMsg:
		if msg.Err != nil {
			m.debugView.AppendLine("Failed to open browser: " + msg.Err.Error())
		}
		if msg.BrowserOpened {
			cmds = append(cmds, m.dialogs.Flash("Opened browser", 3*time.Second))
		} else if msg.URL != "" {
			// No graphical session or browser failed — offer URL for manual copy.
			return m, m.dialogs.CopyToClipboard(msg.URL)
		}

	case daemonLogConnectedMsg:
		cmds = append(cmds, m.streams.OnDaemonLogConnected(msg.ch))

	case daemonLogLineMsg:
		m.debugView.AppendLine(msg.Line)
		cmds = append(cmds, m.streams.ContinueListeningDaemonLog())

	case daemonLogDisconnectedMsg:
		m.debugView.AppendLine("Daemon log stream disconnected. Reconnecting...")
		cmds = append(cmds, m.streams.SubscribeDaemonLogs())

	case TriggerRunMsg:
		action := "Triggered run for"
		if msg.Retry {
			action = "Retried run for"
		}
		m.logActionResult(action, msg.TaskName, msg.Err)
		if msg.Err != nil {
			// Concurrency limit or other error — close exec view to show the task list.
			if m.execView != nil {
				m.closeExecView()
			}
		} else if msg.Run != nil {
			if cmd := m.openExecView(msg.Run); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case StopRunMsg:
		m.logActionResult("Stopped run for", msg.TaskName, msg.Err)

	case reconnectLogMsg:
		if m.execView != nil && m.execView.RunID() == msg.RunID &&
			m.execView.run.Status == model.PhaseRunning {
			cmds = append(cmds, m.streams.StartLogStream(m.execView.run))
		}

	case TickMsg:
		m.execWindow.UpdateVisibleTimes(m.execList.scroll, m.execList.viewportHeight())
		cmds = append(cmds, m.tickCmd())
		if m.execList.NeedsFetch() {
			cmds = append(cmds, m.fetchExecWindow())
		}
		if m.sidebar.ActivePage() == PageInfo {
			cmds = append(cmds, m.streams.FetchSystemStats())
		}

	case quitMsg:
		m.streams.Shutdown()
		m.quitAction = msg.Action
		if msg.Action == QuitShutdownDaemon && m.shutdownFunc != nil {
			return m, m.startShutdownSpinner()
		}
		return m, tea.Quit

	case flashExpiredMsg:
		m.dialogs.ClearFlashIfExpired()

	case systemStatsMsg:
		if msg.Err == nil && msg.Stats != nil {
			m.infoView.UpdateStats(msg.Stats)
		}

	case metricsHistoryMsg:
		if msg.Err == nil && msg.Samples != nil {
			m.infoView.LoadHistory(msg.Samples)
		}

	case runSummaryMsg:
		if msg.Err == nil && msg.Summary != nil {
			m.infoView.UpdateRunSummary(msg.Summary)
		}
	}

	return m, tea.Batch(cmds...)
}

// handleSSEEvent processes a parsed SSE run event.
func (m *Model) handleSSEEvent(evt apiclient.RunStreamEvent) tea.Cmd {
	var runEvt struct {
		Run      *model.Run     `json:"run"`
		TaskName string         `json:"task_name"`
		Status   model.RunPhase `json:"status"`
	}
	if err := json.Unmarshal(evt.Data, &runEvt); err != nil {
		m.debugView.AppendLine("Failed to parse event: " + err.Error())
		return nil
	}

	if runEvt.Run != nil {
		m.execWindow.UpsertRun(*runEvt.Run)

		// Update exec view if watching this run.
		if m.execView != nil && m.execView.RunID() == runEvt.Run.ID {
			prevStatus := m.execView.run.Status
			m.execView.run = runEvt.Run
			if runEvt.Run.Status == model.PhaseRunning && prevStatus != model.PhaseRunning {
				return m.streams.StartLogStream(runEvt.Run)
			}
		}

		// Auto-open when a long-running task starts and user is viewing that task.
		if m.execView == nil && runEvt.Run.Status == model.PhaseRunning &&
			m.sidebar.ActiveTask() == runEvt.TaskName &&
			m.isLongRunningTask(runEvt.TaskName) {
			return m.openExecView(runEvt.Run)
		}
	}

	return nil
}

const logReconnectDelay = 500 * time.Millisecond

// execConfirmCmd runs a confirm-dialog callback synchronously and returns the
// appropriate tea.Cmd. For quit callbacks this handles the quit inline —
// calling Shutdown and returning tea.Quit directly — which prevents the quit
// message from being starved by mouse-motion events that flood BubbleTea's
// unbuffered message channel when WithMouseAllMotion is enabled.
//
// closed indicates whether the dialog signalled it should close. When the
// callback triggers a shutdown we keep the dialog open and transition it into
// the spinner state; otherwise we dismiss it.
func (m *Model) execConfirmCmd(cmd tea.Cmd, closed bool) tea.Cmd {
	result := cmd()
	if result == nil {
		if closed {
			m.dialogs.DismissConfirm()
		}
		return nil
	}
	if qm, ok := result.(quitMsg); ok {
		m.streams.Shutdown()
		m.quitAction = qm.Action
		if qm.Action == QuitShutdownDaemon && m.shutdownFunc != nil {
			return m.startShutdownSpinner()
		}
		m.dialogs.DismissConfirm()
		return tea.Quit
	}
	if closed {
		m.dialogs.DismissConfirm()
	}
	return func() tea.Msg { return result }
}

// startShutdownSpinner transitions the confirm dialog into the shutting-down
// spinner state and fires the shutdown function as a background command.
// If no dialog is open (e.g. quitMsg arrived without a confirm), a new one is
// created so the spinner has somewhere to render.
func (m *Model) startShutdownSpinner() tea.Cmd {
	if !m.dialogs.HasConfirm() {
		dialog := NewConfirmDialog("Quit", "", nil)
		m.dialogs.ShowConfirm(dialog)
	}
	spinnerCmd := m.dialogs.StartShutdown()
	shutdownFn := m.shutdownFunc
	shutdownCmd := func() tea.Msg {
		err := shutdownFn()
		return shutdownDoneMsg{Err: err}
	}
	return tea.Batch(tea.ClearScreen, spinnerCmd, shutdownCmd)
}

// scheduleLogReconnect returns a delayed command to reconnect the log stream.
// Used when a stream ends while the run is still running (e.g. server-side
// timeout or transient disconnect).
func (m *Model) scheduleLogReconnect(runID string) tea.Cmd {
	return tea.Tick(logReconnectDelay, func(time.Time) tea.Msg {
		return reconnectLogMsg{RunID: runID}
	})
}
