// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package uikit

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
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

// DeleteRunMsg is the result of a "Delete Run" action.
type DeleteRunMsg struct {
	RunID    string
	TaskName string
	Err      error
}

// RestartServiceMsg is the result of a "Restart Service" action.
type RestartServiceMsg struct {
	TaskName string
	Err      error
}

// StopServiceMsg is the result of a "Stop Service" action.
type StopServiceMsg struct {
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

// QuitMsg is sent when the user confirms a quit action.
type QuitMsg struct {
	Action QuitAction
}

// FlashExpiredMsg signals that the flash message should be cleared.
type FlashExpiredMsg struct{}

// SSEConnectedMsg is sent when the SSE stream is established.
type SSEConnectedMsg struct {
	Ch <-chan apiclient.RunStreamEvent
}

// SSEEventMsg wraps a parsed SSE run event.
type SSEEventMsg struct {
	Event apiclient.RunStreamEvent
}

// SSEDisconnectedMsg signals the SSE stream dropped.
type SSEDisconnectedMsg struct{}

// ExecWindowFetchedMsg delivers results from an execution window fetch.
type ExecWindowFetchedMsg struct {
	Items  []ExecListItem
	Offset int
	Total  int
}

// LogOlderLoadedMsg delivers the result of a scroll-up REST page fetch.
type LogOlderLoadedMsg struct {
	RunID     string
	Lines     []server.LogLineEntry
	FirstLine int64
	Total     int64
}

// LogStreamConnectedMsg signals that the log SSE stream is connected.
type LogStreamConnectedMsg struct {
	RunID string
	Ch    <-chan apiclient.LogStreamMsg
}

// LogLineMsg delivers one absolute-numbered line from the SSE stream.
type LogLineMsg struct {
	RunID string
	Line  server.LogLineEntry
}

// LogRegionMsg delivers a live snapshot of a still-animating output region
// (a `\r` progress bar or multi-line ANSI redraw). It is never persisted; the
// pane holds it in a transient overlay keyed by stream, replacing the whole
// frame on each message. Empty Rows clears the overlay for that stream.
type LogRegionMsg struct {
	RunID  string
	Stream string
	Epoch  int
	Rows   []string
}

// LogRotatedMsg signals server-side rotation; lines below FirstAvailable
// have been dropped on disk.
type LogRotatedMsg struct {
	RunID          string
	FirstAvailable int64
}

// LogDroppedMsg signals streamer-side backpressure; Count line events were
// discarded after line After.
type LogDroppedMsg struct {
	RunID string
	After int64
	Count int64
}

// LogDoneMsg signals log streaming ended.
type LogDoneMsg struct {
	RunID     string
	FinalLine int64
}

// ReconnectLogMsg is a delayed signal to retry log streaming after a disconnect.
type ReconnectLogMsg struct {
	RunID string
}

// SystemStatsMsg delivers live system resource metrics.
type SystemStatsMsg struct {
	Stats *model.SystemStats
	Err   error
}

// DaemonInfoMsg delivers a fresh /api/info read — the TUI polls it to keep
// the config-stale notice current while the operator edits runwisp.toml.
type DaemonInfoMsg struct {
	Info *model.DaemonInfo
	Err  error
}

// MetricsHistoryMsg delivers historical metrics for sparkline pre-fill.
type MetricsHistoryMsg struct {
	Samples []model.MetricsSample
	Err     error
}

// RunSummaryMsg delivers aggregate run statistics.
type RunSummaryMsg struct {
	Summary *model.RunSummary
	Err     error
}

// DaemonLogConnectedMsg signals that the daemon log SSE stream is established.
type DaemonLogConnectedMsg struct {
	Ch <-chan string
}

// DaemonLogLineMsg delivers a single daemon log line from the SSE stream.
type DaemonLogLineMsg struct {
	Line string
}

// DaemonLogDisconnectedMsg signals the daemon log stream dropped.
type DaemonLogDisconnectedMsg struct{}

// OpenBrowserMsg is a command result indicating the browser was opened (or failed).
type OpenBrowserMsg struct {
	URL           string // launch URL (empty only if ticket generation failed)
	BrowserOpened bool   // true when openBrowser was called and returned nil
	Err           error  // ticket generation error or browser open error
}

// ShutdownDoneMsg signals that the daemon shutdown completed; triggers tea.Quit.
type ShutdownDoneMsg struct {
	Err error
}

// SpinnerTickMsg wraps the bubbles spinner tick for the confirm dialog.
type SpinnerTickMsg struct {
	Inner tea.Msg
}

// NotificationStreamConnectedMsg signals the notifications SSE stream is established.
type NotificationStreamConnectedMsg struct {
	Ch <-chan apiclient.NotificationStreamEvent
}

// NotificationEventMsg wraps a parsed notification SSE event.
type NotificationEventMsg struct {
	Event apiclient.NotificationStreamEvent
}

// NotificationStreamDisconnectedMsg signals the notifications stream dropped.
type NotificationStreamDisconnectedMsg struct{}

// OpenRunMsg requests the model open an exec view for the given run. Used by
// asynchronous run lookups (e.g., notification → exec view) where the run is
// not in the local execWindow cache yet.
type OpenRunMsg struct {
	Run *model.Run
}

// NotificationUnreadCountMsg delivers the snapshot unread count fetched at
// startup. It seeds the panel's badge before the first list page resolves so
// the operator sees the right number even when older unread items live
// beyond the loaded slice.
type NotificationUnreadCountMsg struct {
	Count int64
	Err   error
}

// NotificationsLoadedMsg delivers the initial page of notifications fetched
// at startup so the expanded panel is populated immediately, instead of
// waiting for a fresh SSE event.
type NotificationsLoadedMsg struct {
	Items []server.NotificationDTO
	Err   error
}

// NotificationReadStateMsg is the result of a per-notification mark-read or
// mark-unread action. The TUI applies an optimistic local update before
// firing the API call, so this message only logs failures (and resyncs from
// the server when one occurs).
type NotificationReadStateMsg struct {
	ID   string
	Read bool
	Err  error
}

// NotificationBoundaryFlashClearedMsg nudges the panel to repaint after a
// boundary-flash duration elapses so the cursor row returns to its normal
// background.
type NotificationBoundaryFlashClearedMsg struct{}

// LogLineHistoryMsg delivers the prior whole-region frames an anchor line
// animated through, fetched on demand when the operator opens the frame-history
// viewer. Frames is nil/empty when none are available; Committed is the line's
// final on-disk text.
type LogLineHistoryMsg struct {
	RunID     string
	Line      int64
	Frames    [][]string
	Committed string
	Err       error
}
