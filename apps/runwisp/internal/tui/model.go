// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"net"
	"net/url"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/home"
	"github.com/runwisp/runwisp/internal/tui/views/info"
	"github.com/runwisp/runwisp/internal/tui/views/logsearch"
	"github.com/runwisp/runwisp/internal/tui/views/notifications"
)

const (
	tickInterval  = 1 * time.Second
	maxDebugLines = 500
	// infoPollInterval paces the /api/info refresh that keeps the
	// config-stale notice live. The probe re-hashes a handful of small
	// files over the local socket — cheap, but no need to do it every tick.
	infoPollInterval = 5 * time.Second
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
	logSearch     *logsearch.Model
	// pendingHighlight, when non-zero, is the absolute log-line number the
	// next exec-view fetch should land on. Set when a search hit is
	// selected for a run that's not yet open; consumed by the log
	// streamer/fetcher when the buffer is ready.
	pendingHighlight int64

	panelFocus uikit.PanelFocus
	info       uikit.StartupInfo
	client     *apiclient.Client

	// Home page cursor for interactive fields (-1 = not in header area).
	homeCursor int
	// Cached header geometry, recomputed by recalcExecListHeight.
	layout headerLayout

	// Quit action chosen by the user (keep daemon or shut down).
	quitAction   uikit.QuitAction
	shutdownFunc func() error
	// shutdownErr records a failed daemon shutdown so the caller can report it
	// instead of the TUI closing as if the daemon had stopped.
	shutdownErr      error
	launchTicketFunc func() (string, error)

	mouse mouseState

	// lastInfoFetch paces the periodic /api/info poll (see infoPollInterval).
	lastInfoFetch time.Time

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

	m := Model{
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
	// serviceInstances reads only the immutable info.Tasks snapshot, so this
	// closure stays correct across the value-copied model.
	m.execList.SetInstanceCountLookup(m.serviceInstances)
	return m
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
func (m *Model) openExecView(run *model.Run) tea.Cmd {
	if latest := m.execWindow.FindRun(run.ID); latest != nil {
		run = latest
	}
	ev := execlist.NewExecView(run)
	ev.TaskIsService = m.isService(run.TaskName)
	ev.InstanceCount = m.serviceInstances(run.TaskName)
	mainW, mainH := m.mainSize()
	ev.SetSize(mainW, mainH)
	ev.SetFocused(true)
	ev.HeaderFocus = execlist.HeaderFocusBack
	m.execView = &ev
	m.execList.SetFocused(false)

	cmds := []tea.Cmd{m.markRunNotificationsRead(run.ID)}
	if run.Status != model.PhasePending {
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

// markAllNotificationsRead optimistically clears every unread notification and
// the badge, then persists the change. No-op when nothing is unread.
func (m *Model) markAllNotificationsRead() tea.Cmd {
	if !m.notifications.MarkAllReadLocal(time.Now()) {
		return nil
	}
	m.updateLayout()
	return m.streams.MarkAllNotificationsRead()
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
	w = max(w, 20)
	h := max(m.height-1, 5) // -1 for help bar
	return w, h
}

// contentWidth is the main panel width bounded by MaxContentWidth so the home
// header and exec table stay left-aligned and don't stretch on wide terminals.
func (m *Model) contentWidth() int {
	mainW, _ := m.mainSize()
	return min(mainW, uikit.MaxContentWidth)
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
	panelW := m.contentWidth()
	m.notifications.SetWidth(panelW)
	if m.sidebar.ActivePage() == uikit.PageHome {
		listH -= m.notifications.PanelHeight()
	}
	if listH < 5 {
		listH = 5
	}
	m.execList.SetSize(panelW, listH)
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
func (m *Model) latestRunningExec(taskName string) *model.Run {
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

// ShutdownErr returns the error from a shutdown-on-exit attempt, or nil when the
// daemon stopped cleanly (or the user kept it running).
func (m Model) ShutdownErr() error {
	return m.shutdownErr
}

// showQuitConfirm displays the quit dialog with keep-daemon/shutdown options.
// A service-managed daemon (systemd / launchd) gets no "Shut Down" option —
// SIGTERM from the TUI would desync the manager — just a `runwisp stop` hint.
func (m *Model) showQuitConfirm() {
	if m.info.ServiceManaged {
		dialog := NewChoiceDialog(
			"Quit",
			"The daemon keeps running in the background.",
			"Quit TUI",
			"Cancel",
			func() tea.Msg { return uikit.QuitMsg{Action: uikit.QuitKeepDaemon} },
			nil,
		).WithNote(
			"",
			"Managed by "+serviceManagerLabel()+" — to stop it, run:",
			"runwisp stop",
		)
		m.dialogs.ShowConfirm(dialog)
		return
	}

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

// serviceManagerLabel names the init system for dialog copy. The TUI talks
// to the daemon over its local Unix socket, so GOOS here matches the host
// the daemon runs under.
func serviceManagerLabel() string {
	switch runtime.GOOS {
	case "linux":
		return "systemd"
	case "darwin":
		return "launchd"
	default:
		return "a service manager"
	}
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

// showRunParams opens the read-only run-params modal for the focused run.
// No-op when the run has no resolved parameters.
func (m *Model) showRunParams() tea.Cmd {
	if m.execView == nil || m.execView.Run == nil || len(m.execView.Run.Params) == 0 {
		return nil
	}
	run := m.execView.Run
	m.dialogs.ShowRunParams(NewRunParamsDialog(run.TaskName, run.Params))
	return m.dialogs.SyncMouseState()
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
		return m.dialogs.CopyToClipboard(m.info.WebURL())
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
//
// A launch ticket redeems into a full session, so over a plain-HTTP remote
// connection it would travel in clear text. When the resolved Web UI base is
// insecure (http:// to a non-loopback host) we gate the action behind a
// confirmation dialog; loopback and https bases open directly.
func (m *Model) openLaunchURL(target string) tea.Cmd {
	if m.launchTicketFunc == nil {
		return nil
	}
	if target == "" {
		target = "/"
	}
	base := m.info.WebURL()
	if isInsecureRemoteURL(base) {
		return m.showConfirmDialog(
			"Insecure connection",
			fmt.Sprintf("The Web UI at\n%s\nuses unencrypted HTTP. Opening it sends a single-use\nsession ticket in clear text over the network.\n\nContinue anyway?", base),
			m.launchBrowserCmd(base, target),
		)
	}
	return m.launchBrowserCmd(base, target)
}

// launchBrowserCmd mints a launch ticket and opens (or offers) the resulting
// URL built from base. Factored out of openLaunchURL so it can be deferred
// behind the insecure-connection confirmation dialog.
func (m *Model) launchBrowserCmd(base, target string) tea.Cmd {
	ticketFunc := m.launchTicketFunc
	return func() tea.Msg {
		ticket, err := ticketFunc()
		if err != nil {
			return uikit.OpenBrowserMsg{Err: err}
		}
		launchURL := fmt.Sprintf("%s/api/auth/launch?ticket=%s&redirect=%s",
			strings.TrimRight(base, "/"), ticket, url.QueryEscape(target))
		if !canOpenBrowser() {
			return uikit.OpenBrowserMsg{URL: launchURL}
		}
		if err := openBrowser(launchURL); err != nil {
			return uikit.OpenBrowserMsg{URL: launchURL, Err: err}
		}
		return uikit.OpenBrowserMsg{URL: launchURL, BrowserOpened: true}
	}
}

// isInsecureRemoteURL reports whether base is an http:// URL pointing at a
// non-loopback host — the case where redeeming a launch ticket would expose
// it in clear text over the network. A loopback host (localhost / 127/8 /
// ::1) or an https:// base is considered safe.
func isInsecureRemoteURL(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return false
	}
	return true
}

func (m *Model) logActionResult(action, taskName string, err error) {
	if err != nil {
		m.debugView.AppendLine(fmt.Sprintf("%s failed for %s: %s", action, taskName, err))
	} else {
		m.debugView.AppendLine(fmt.Sprintf("%s %s", action, taskName))
	}
}
