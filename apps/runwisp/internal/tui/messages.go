// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
)

// Page identifies which content the main panel displays.
type Page int

const (
	PageHome Page = iota
	PageInfo
	PageDebug
)

func (p Page) String() string {
	switch p {
	case PageHome:
		return "Home"
	case PageInfo:
		return "Info"
	case PageDebug:
		return "Debug"
	default:
		return "Unknown"
	}
}

// PanelFocus tracks which panel has keyboard focus.
type PanelFocus int

const (
	PanelSidebar PanelFocus = iota
	PanelMain
)

// logBatchMsg delivers multiple log lines at once for efficiency.
type logBatchMsg struct {
	RunID string
	Lines []string
}

// TickMsg drives periodic polling/refresh.
type TickMsg struct{}

// ExecListItem holds pre-formatted data for an execution row.
type ExecListItem struct {
	Run      model.Run
	Duration string
	TimeAgo  string
}

type DebugLogMsg struct {
	Message string
}

// TriggerRunMsg is the result of a "Run Now" or "Retry" action.
type TriggerRunMsg struct {
	TaskName string
	Run      *model.Run
	Err      error
	Retry    bool
}

// StopRunMsg is the result of a "Stop" action.
type StopRunMsg struct {
	RunID    string
	TaskName string
	Err      error
}

// RestartServiceMsg is the result of a "Restart Service" action.
type RestartServiceMsg struct {
	TaskName string
	Err      error
}

// QuitAction specifies what should happen to the daemon when the TUI exits.
type QuitAction int

const (
	// QuitKeepDaemon leaves the daemon running in the background.
	QuitKeepDaemon QuitAction = iota
	// QuitShutdownDaemon stops the daemon on exit.
	QuitShutdownDaemon
)

// quitMsg is sent when the user confirms a quit action.
type quitMsg struct {
	Action QuitAction
}

// flashExpiredMsg signals that the flash message should be cleared.
type flashExpiredMsg struct{}

// sseConnectedMsg is sent when the SSE stream is established.
type sseConnectedMsg struct {
	ch <-chan apiclient.RunStreamEvent
}

// sseEventMsg wraps a parsed SSE run event.
type sseEventMsg struct {
	Event apiclient.RunStreamEvent
}

// sseDisconnectedMsg signals the SSE stream dropped.
type sseDisconnectedMsg struct{}

// execWindowFetchedMsg delivers results from an execution window fetch.
type execWindowFetchedMsg struct {
	Items  []ExecListItem
	Offset int
	Total  int
}

// logStreamConnectedMsg signals that the log SSE stream is connected.
type logStreamConnectedMsg struct {
	RunID string
	Ch    <-chan string
}

// logChunkMsg delivers a raw log chunk from the SSE stream.
type logChunkMsg struct {
	RunID string
	Chunk string
}

// logDoneMsg signals log streaming ended.
type logDoneMsg struct {
	RunID string
}

// reconnectLogMsg is a delayed signal to retry log streaming after a disconnect.
type reconnectLogMsg struct {
	RunID string
}

// systemStatsMsg delivers live system resource metrics.
type systemStatsMsg struct {
	Stats *model.SystemStats
	Err   error
}

// metricsHistoryMsg delivers historical metrics for sparkline pre-fill.
type metricsHistoryMsg struct {
	Samples []model.MetricsSample
	Err     error
}

// runSummaryMsg delivers aggregate run statistics.
type runSummaryMsg struct {
	Summary *apiclient.RunSummary
	Err     error
}

// daemonLogConnectedMsg signals that the daemon log SSE stream is established.
type daemonLogConnectedMsg struct {
	ch <-chan string
}

// daemonLogLineMsg delivers a single daemon log line from the SSE stream.
type daemonLogLineMsg struct {
	Line string
}

// daemonLogDisconnectedMsg signals the daemon log stream dropped.
type daemonLogDisconnectedMsg struct{}

// openBrowserMsg is a command result indicating the browser was opened (or failed).
type openBrowserMsg struct {
	URL           string // launch URL (empty only if ticket generation failed)
	BrowserOpened bool   // true when openBrowser was called and returned nil
	Err           error  // ticket generation error or browser open error
}

// shutdownDoneMsg signals that the daemon shutdown completed; triggers tea.Quit.
type shutdownDoneMsg struct {
	Err error
}

// spinnerTickMsg wraps the bubbles spinner tick for the confirm dialog.
type spinnerTickMsg struct {
	Inner tea.Msg
}

// notificationStreamConnectedMsg signals the notifications SSE stream is established.
type notificationStreamConnectedMsg struct {
	ch <-chan apiclient.NotificationStreamEvent
}

// notificationEventMsg wraps a parsed notification SSE event.
type notificationEventMsg struct {
	Event apiclient.NotificationStreamEvent
}

// notificationStreamDisconnectedMsg signals the notifications stream dropped.
type notificationStreamDisconnectedMsg struct{}

// openRunMsg requests the model open an exec view for the given run. Used by
// asynchronous run lookups (e.g., notification → exec view) where the run is
// not in the local execWindow cache yet.
type openRunMsg struct {
	Run *model.Run
}

// notificationUnreadCountMsg delivers the snapshot unread count fetched at
// startup. It seeds the panel's badge before the first list page resolves so
// the operator sees the right number even when older unread items live
// beyond the loaded slice.
type notificationUnreadCountMsg struct {
	Count int64
	Err   error
}

// notificationsLoadedMsg delivers the initial page of notifications fetched
// at startup so the expanded panel is populated immediately, instead of
// waiting for a fresh SSE event.
type notificationsLoadedMsg struct {
	Items []apiclient.Notification
	Err   error
}

// notificationReadStateMsg is the result of a per-notification mark-read or
// mark-unread action. The TUI applies an optimistic local update before
// firing the API call, so this message only logs failures (and resyncs from
// the server when one occurs).
type notificationReadStateMsg struct {
	ID   string
	Read bool
	Err  error
}

// notificationBoundaryFlashClearedMsg nudges the panel to repaint after a
// boundary-flash duration elapses so the cursor row returns to its normal
// background.
type notificationBoundaryFlashClearedMsg struct{}
