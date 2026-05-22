// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server/dto"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/home"
	"github.com/runwisp/runwisp/internal/tui/views/info"
	"github.com/runwisp/runwisp/internal/tui/views/notifications"
)

const (
	tickInterval  = 1 * time.Second
	maxDebugLines = 500
)

// mouseState tracks mouse position and double-click detection.
type mouseState struct {
	lastClickTime time.Time
	lastClickY    int
	hoverX        int
	hoverY        int
	homeHover     int
}

// headerLayout caches header geometry computed by recalcExecListHeight.
// Values are invalidated (recomputed) on every WindowSizeMsg or active-task change.
type headerLayout struct {
	taskH       int // newline count of the task header
	taskBtnY    int // Y line of the Run Now button within the task header
	homeH       int // newline count of the home header
	homeFieldsY int // Y line where the first interactive home field begins
}

// Model is the root Bubble Tea model for the RunWisp TUI.
type Model struct {
	sidebar    home.Sidebar
	execList   execlist.ExecList
	execWindow *execlist.ExecWindow
	execView   *execlist.ExecView
	infoView   *info.InfoView
	debugView  DebugView

	dialogs       DialogManager
	streams       StreamManager
	notifications notifications.Panel

	panelFocus uikit.PanelFocus
	info       uikit.StartupInfo
	client     *apiclient.Client

	// Home page cursor for interactive fields (-1 = not in header area).
	homeCursor int
	// Cached header geometry, recomputed by recalcExecListHeight.
	layout headerLayout

	// Quit action chosen by the user (keep daemon or shut down).
	quitAction       uikit.QuitAction
	shutdownFunc     func() error
	launchTicketFunc func() (string, error)

	mouse mouseState

	width    int
	height   int
	ready    bool
	isRemote bool
}

// TUIConfig holds the dependencies needed to start the interactive TUI.
type TUIConfig struct {
	Info             uikit.StartupInfo
	Client           *apiclient.Client
	IsRemote         bool                   // true when connecting to a remote daemon (no local debug writer)
	ShutdownFunc     func() error           // if set, called inside the TUI to shut down the daemon
	LaunchTicketFunc func() (string, error) // generates a single-use launch ticket for browser auth
}

func NewModel(cfg TUIConfig) Model {
	sidebar := home.NewSidebar("RunWisp", cfg.Info.Version, cfg.Info.Fingerprint, cfg.Info.Tasks)
	execWindow := execlist.NewExecWindow(cfg.Client)
	execList := execlist.NewExecList(execWindow)
	infoView := info.NewInfoView(cfg.Info)
	debugView := NewDebugView()

	return Model{
		sidebar:          sidebar,
		execList:         execList,
		execWindow:       execWindow,
		infoView:         &infoView,
		debugView:        debugView,
		dialogs:          DialogManager{},
		streams:          NewStreamManager(cfg.Client),
		notifications:    notifications.NewPanel(),
		panelFocus:       uikit.PanelSidebar,
		info:             cfg.Info,
		client:           cfg.Client,
		homeCursor:       -1,
		mouse:            mouseState{homeHover: -1},
		isRemote:         cfg.IsRemote,
		shutdownFunc:     cfg.ShutdownFunc,
		launchTicketFunc: cfg.LaunchTicketFunc,
	}
}

// Init loads initial data and starts subscriptions.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.streams.FetchExecWindow(m.execWindow, m.execList.Scroll, m.execList.ViewportHeight()),
		m.streams.SubscribeEvents(),
		m.streams.SubscribeNotifications(),
		m.streams.FetchUnreadCount(),
		m.streams.FetchNotifications(),
		m.tickCmd(),
	}
	if m.isRemote {
		cmds = append(cmds, m.streams.SubscribeDaemonLogs())
	}
	return tea.Batch(cmds...)
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg {
		return uikit.TickMsg{}
	})
}

// openExecView creates the execution detail view for the given run.
// It checks the exec window for a more recent version of the run
// (SSE events may arrive before the API response that triggered this call).
// Any unread notifications attached to this run are marked read — opening the
// run is the operator acknowledging it.
// Returns a tea.Cmd to start log streaming if the run is active or completed.
func (m *Model) openExecView(run *dto.Run) tea.Cmd {
	if latest := m.execWindow.FindRun(run.ID); latest != nil {
		run = latest
	}
	ev := execlist.NewExecView(run)
	ev.TaskIsService = m.isService(run.TaskName)
	mainW, mainH := m.mainSize()
	ev.SetSize(mainW, mainH)
	ev.SetFocused(true)
	ev.HeaderFocus = execlist.HeaderFocusBack
	m.execView = &ev
	m.execList.SetFocused(false)

	cmds := []tea.Cmd{m.markRunNotificationsRead(run.ID)}
	if run.Status != sqlcdb.PhasePending {
		cmds = append(cmds, m.streams.StartLogStream(run, -int64(execlist.LogTailLines)))
	}
	return tea.Batch(cmds...)
}

// markRunNotificationsRead applies an optimistic local mark-read to every
// known notification tied to runID and fires per-row API calls. Returns nil
// when nothing matches so the caller doesn't pay for an empty batch.
func (m *Model) markRunNotificationsRead(runID string) tea.Cmd {
	ids := m.notifications.UnreadIDsForRun(runID)
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	cmds := make([]tea.Cmd, 0, len(ids))
	for _, id := range ids {
		m.notifications.MarkReadLocal(id, now)
		if cmd := m.streams.MarkNotificationRead(id); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// toggleSelectedNotificationRead flips the read state of the cursor row in
// the expanded notifications panel. Optimistically updates the local store
// and returns a command that persists the change to the daemon.
func (m *Model) toggleSelectedNotificationRead() tea.Cmd {
	sel := m.notifications.Selected()
	if sel == nil {
		return nil
	}
	if sel.ReadAt == nil {
		m.notifications.MarkReadLocal(sel.ID, time.Now())
		return m.streams.MarkNotificationRead(sel.ID)
	}
	m.notifications.MarkUnreadLocal(sel.ID)
	return m.streams.MarkNotificationUnread(sel.ID)
}

func (m *Model) closeExecView() tea.Cmd {
	m.streams.CancelLogStream()
	m.execView = nil
	if m.panelFocus == uikit.PanelMain {
		m.execList.SetFocused(true)
	}
	return m.syncMouseState()
}

// isExecFullscreen reports whether the exec view is showing in fullscreen mode.
func (m *Model) isExecFullscreen() bool {
	return m.execView != nil && m.execView.Fullscreen()
}

// syncMouseState refreshes the mouse-hold derived from non-dialog state
// (currently: fullscreen log) and dispatches any enable/disable command.
func (m *Model) syncMouseState() tea.Cmd {
	m.dialogs.SetMouseHold(m.isExecFullscreen())
	return m.dialogs.SyncMouseState()
}

// fetchExecWindow is a convenience wrapper for StreamManager.
func (m *Model) fetchExecWindow() tea.Cmd {
	return m.streams.FetchExecWindow(m.execWindow, m.execList.Scroll, m.execList.ViewportHeight())
}

func (m *Model) updateLayout() {
	mainW, mainH := m.mainSize()
	m.sidebar.SetSize(uikit.SidebarWidth, m.height-1) // -1 for help bar
	m.infoView.SetSize(mainW, mainH)
	m.debugView.SetSize(mainW, mainH)
	if m.execView != nil {
		m.execView.SetSize(mainW, mainH)
	}
	m.recalcExecListHeight()
}

func (m *Model) mainSize() (int, int) {
	w := m.width
	if !m.isExecFullscreen() {
		w -= uikit.SidebarWidth
	}
	if w < 20 {
		w = 20
	}
	h := m.height - 1 // help bar
	if h < 5 {
		h = 5
	}
	return w, h
}

// recalcExecListHeight adjusts the exec list height based on the active view header.
func (m *Model) recalcExecListHeight() {
	mainW, mainH := m.mainSize()
	listH := mainH
	if m.sidebar.ActivePage() == uikit.PageHome || m.sidebar.ActiveTask() != "" {
		if m.sidebar.ActiveTask() != "" {
			header, btnY := home.RenderTaskHeader(m.sidebar.ActiveTask(), m.taskDisplayByName(m.sidebar.ActiveTask()), mainW, false)
			m.layout.taskBtnY = btnY
			m.layout.taskH = strings.Count(header, "\n")
			listH -= m.layout.taskH
		} else {
			header, fieldsStartY := home.RenderHeader(m.info, m.hasLaunchTicket(), mainW, -1, -1)
			m.layout.homeH = strings.Count(header, "\n")
			m.layout.homeFieldsY = fieldsStartY
			listH -= m.layout.homeH
		}
	}
	m.notifications.SetWidth(mainW)
	if m.sidebar.ActivePage() == uikit.PageHome {
		listH -= m.notifications.PanelHeight()
	}
	if listH < 5 {
		listH = 5
	}
	m.execList.SetSize(mainW, listH)
}

// taskDisplayByName looks up a task definition by name.
func (m *Model) taskDisplayByName(name string) *model.TaskBrief {
	for i := range m.info.Tasks {
		if m.info.Tasks[i].Name == name {
			return &m.info.Tasks[i]
		}
	}
	return nil
}

// isSingleInstanceService reports whether the task is a service with exactly one instance.
// Used to decide whether to auto-open its log view on start.
func (m *Model) isSingleInstanceService(name string) bool {
	task := m.taskDisplayByName(name)
	return task != nil && task.Kind.IsService() && task.Instances <= 1
}

// isService reports whether the named task is configured as an always-on service.
func (m *Model) isService(name string) bool {
	task := m.taskDisplayByName(name)
	return task != nil && task.Kind.IsService()
}

// serviceInstances returns the configured instance count for a service, or 0 if not a service.
func (m *Model) serviceInstances(name string) int {
	task := m.taskDisplayByName(name)
	if task == nil || !task.Kind.IsService() {
		return 0
	}
	if task.Instances < 1 {
		return 1
	}
	return task.Instances
}

// latestRunningExec returns the most recent running execution for the given task, if any.
func (m *Model) latestRunningExec(taskName string) *dto.Run {
	return m.execWindow.LatestRunning(taskName)
}

// autoOpenService opens the latest running execution for single-instance services.
// Returns a command if a log stream should be started, or nil.
func (m *Model) autoOpenService(taskName string) tea.Cmd {
	if !m.isSingleInstanceService(taskName) {
		return nil
	}
	if run := m.latestRunningExec(taskName); run != nil {
		return m.openExecView(run)
	}
	return nil
}

// QuitAction returns the quit action chosen by the user.
func (m Model) QuitAction() uikit.QuitAction {
	return m.quitAction
}

// showQuitConfirm displays the quit dialog with keep-daemon/shutdown options.
func (m *Model) showQuitConfirm() {
	dialog := NewChoiceDialog(
		"Quit",
		"Keep the daemon running in the background?",
		"Keep Running",
		"Shut Down",
		func() tea.Msg { return uikit.QuitMsg{Action: uikit.QuitKeepDaemon} },
		func() tea.Msg { return uikit.QuitMsg{Action: uikit.QuitShutdownDaemon} },
	)
	// Hint about autostart only in a fresh local session (the operator
	// started this daemon by running ./runwisp). A `runwisp tui` client
	// connected to an already-running daemon doesn't need the nudge.
	if !m.isRemote {
		dialog = dialog.WithNote(
			"",
			"Tip: `runwisp service install` makes it",
			"survive a reboot (systemd / launchd).",
		)
	}
	m.dialogs.ShowConfirm(dialog)
}

// resolveTaskName determines which task to act on based on current focus.
func (m *Model) resolveTaskName() string {
	if m.panelFocus == uikit.PanelMain {
		return m.sidebar.ActiveTask()
	}
	name := m.sidebar.CursorTaskName()
	if name == "" {
		name = m.sidebar.ActiveTask()
	}
	return name
}

// copyExecField copies the focused meta field value in the execution detail view.
func (m *Model) copyExecField() tea.Cmd {
	if m.execView == nil {
		return nil
	}
	value := m.execView.CopyableValue()
	if value == "" {
		return nil
	}
	return m.dialogs.CopyToClipboard(value)
}

// hasLaunchTicket reports whether the one-click browser-open action is available.
func (m *Model) hasLaunchTicket() bool {
	return m.launchTicketFunc != nil
}

// openRunByID opens the exec view for a run identified by task + run ID.
// Looks the run up in the in-memory window first; falls back to a REST call.
func (m *Model) openRunByID(taskName, runID string) tea.Cmd {
	if run := m.execWindow.FindRun(runID); run != nil {
		return m.openExecView(run)
	}
	if m.client == nil {
		return nil
	}
	client := m.client
	return func() tea.Msg {
		run, err := client.GetRun(taskName, runID)
		if err != nil {
			return uikit.DebugLogMsg{Message: fmt.Sprintf("Failed to load run %s: %s", runID, err.Error())}
		}
		return uikit.OpenRunMsg{Run: run}
	}
}

// activateHomeField performs the primary action for the currently selected home field:
// opens the browser for "Open Web UI", copies to clipboard for URL/password fields.
func (m *Model) activateHomeField() tea.Cmd {
	fields := home.Fields(m.info, m.hasLaunchTicket())
	if m.homeCursor < 0 || m.homeCursor >= len(fields) {
		return nil
	}

	switch fields[m.homeCursor] {
	case home.FieldOpenWebUI:
		return m.openWebUI()
	case home.FieldWebUI:
		return m.dialogs.CopyToClipboard(fmt.Sprintf("http://localhost:%d", m.info.Port))
	case home.FieldPassword:
		return m.dialogs.CopyToClipboard(m.info.Password)
	default:
		return nil
	}
}

// openWebUI generates a launch ticket and opens the browser (or offers the
// URL for manual copy when no graphical session is available, e.g. SSH).
func (m *Model) openWebUI() tea.Cmd {
	return m.openLaunchURL("/")
}

// downloadExecLog opens the raw-log endpoint for the currently focused exec
// view in the operator's browser via a single-use launch ticket. On a
// non-graphical session (e.g. SSH) the URL is offered for clipboard copy
// instead, with the modal dialog as the final fallback.
func (m *Model) downloadExecLog() tea.Cmd {
	if m.execView == nil || m.execView.Run == nil {
		return nil
	}
	run := m.execView.Run
	if run.TaskName == "" || run.ID == "" {
		return nil
	}
	target := fmt.Sprintf("/api/tasks/%s/runs/%s/log/raw",
		url.PathEscape(run.TaskName), url.PathEscape(run.ID))
	return m.openLaunchURL(target)
}

// openLaunchURL builds a one-shot launch URL pointing the daemon at `target`
// (a same-origin absolute path) and routes it through the existing browser
// → clipboard → modal fallback. `target` defaults to "/" when empty.
func (m *Model) openLaunchURL(target string) tea.Cmd {
	if m.launchTicketFunc == nil {
		return nil
	}
	if target == "" {
		target = "/"
	}
	ticketFunc := m.launchTicketFunc
	port := m.info.Port
	return func() tea.Msg {
		ticket, err := ticketFunc()
		if err != nil {
			return uikit.OpenBrowserMsg{Err: err}
		}
		launchURL := fmt.Sprintf("http://localhost:%d/api/auth/launch?ticket=%s&redirect=%s",
			port, ticket, url.QueryEscape(target))
		if !canOpenBrowser() {
			return uikit.OpenBrowserMsg{URL: launchURL}
		}
		if err := openBrowser(launchURL); err != nil {
			return uikit.OpenBrowserMsg{URL: launchURL, Err: err}
		}
		return uikit.OpenBrowserMsg{URL: launchURL, BrowserOpened: true}
	}
}

func (m *Model) logActionResult(action, taskName string, err error) {
	if err != nil {
		m.debugView.AppendLine(fmt.Sprintf("%s failed for %s: %s", action, taskName, err))
	} else {
		m.debugView.AppendLine(fmt.Sprintf("%s %s", action, taskName))
	}
}
