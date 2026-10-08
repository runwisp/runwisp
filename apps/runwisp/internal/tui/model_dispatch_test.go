// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// Every message kind Update knows is routed without panicking, even with
// the cheapest possible payload. Handlers are covered in their own tests.
func TestModelUpdate_HandlesEveryMessageKind(t *testing.T) {
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 30},
		tea.MouseClickMsg{},
		tea.KeyPressMsg{Code: 'x', Text: "x"},
		uikit.ExecWindowFetchedMsg{},
		uikit.SSEConnectedMsg{},
		uikit.SSEEventMsg{},
		uikit.SSEDisconnectedMsg{},
		uikit.LogOlderLoadedMsg{},
		uikit.LogStreamConnectedMsg{},
		uikit.LogLineMsg{},
		uikit.LogRotatedMsg{},
		uikit.LogDroppedMsg{},
		uikit.LogDoneMsg{},
		uikit.DebugLogMsg{},
		uikit.ReconnectLogMsg{},
		uikit.DaemonLogConnectedMsg{},
		uikit.DaemonLogLineMsg{},
		uikit.DaemonLogDisconnectedMsg{},
		uikit.NotificationUnreadCountMsg{},
		uikit.NotificationsLoadedMsg{},
		uikit.NotificationReadStateMsg{},
		uikit.NotificationBoundaryFlashClearedMsg{},
		uikit.TriggerRunMsg{TaskName: "t1"},
		uikit.StopRunMsg{TaskName: "t1"},
		uikit.RestartServiceMsg{TaskName: "t1"},
		uikit.StopServiceMsg{TaskName: "t1"},
		uikit.DeleteRunMsg{TaskName: "t1"},
		uikit.TickMsg{},
		uikit.QuitMsg{Action: uikit.QuitKeepDaemon},
		uikit.FlashExpiredMsg{},
		uikit.OpenBrowserMsg{},
		uikit.OpenRunMsg{},
		uikit.SystemStatsMsg{},
		uikit.MetricsHistoryMsg{},
		uikit.RunSummaryMsg{},
	}
	for _, msg := range msgs {
		m := newTestModel(nil)
		if got, _ := m.Update(msg); got == nil {
			t.Fatalf("Update(%T) did not return a Model", msg)
		}
	}
}

// Update applies a recognised message and falls through to a no-op for an
// unrecognised one.
func TestModelUpdate_RoutesAndFallsThrough(t *testing.T) {
	m := newTestModel(nil)

	got, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	got2 := got.(Model)
	if got2.width != 80 || got2.height != 24 {
		t.Fatalf("WindowSizeMsg not applied: width=%d height=%d", got2.width, got2.height)
	}

	// Unrecognised: arbitrary anonymous struct — must reach the noop tail.
	if _, cmd := m.Update(struct{}{}); cmd != nil {
		t.Fatal("unrecognised msg must produce nil cmd")
	}
}

// ─── handleSystemStats / handleMetricsHistory / handleRunSummary ─────────────

func TestHandleSystemStats_NilStatsOrErrIsNoop(t *testing.T) {
	m := newTestModel(nil)

	// Nil stats — handler must not panic.
	m.handleSystemStats(uikit.SystemStatsMsg{Err: errors.New("boom")})
	m.handleSystemStats(uikit.SystemStatsMsg{Stats: &model.SystemStats{Name: "test"}})
}

func TestHandleMetricsHistory_ErrAndSuccess(t *testing.T) {
	m := newTestModel(nil)
	m.handleMetricsHistory(uikit.MetricsHistoryMsg{Err: errors.New("x")})
	m.handleMetricsHistory(uikit.MetricsHistoryMsg{Samples: []model.MetricsSample{{Timestamp: 1}}})
}

func TestHandleRunSummary_ErrAndSuccess(t *testing.T) {
	m := newTestModel(nil)
	m.handleRunSummary(uikit.RunSummaryMsg{Err: errors.New("x")})
	m.handleRunSummary(uikit.RunSummaryMsg{Summary: &model.RunSummary{Total: 1}})
}

func TestHandleWindowSize_AppliesDimensions(t *testing.T) {
	m := newTestModel(nil)
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.width != 120 || m.height != 40 || !m.ready {
		t.Fatalf("handleWindowSize did not apply dimensions: %+v", m)
	}
}
