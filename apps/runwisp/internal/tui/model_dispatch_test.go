// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// The dispatchers are pure structural switches: given a message they call the
// matching handler and report intercepted=true. The negative path returns
// intercepted=false. These tests exercise each branch with the cheapest
// possible payload — handlers themselves are covered elsewhere or via the
// happy-path assertions here.

func TestDispatchInputMsg(t *testing.T) {
	m := newTestModel(nil)

	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"WindowSize", tea.WindowSizeMsg{Width: 100, Height: 30}},
		{"Mouse", tea.MouseMsg{Type: tea.MouseUnknown}},
		{"Key", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok := m.dispatchInputMsg(tc.msg)
			if !ok {
				t.Fatalf("dispatchInputMsg(%T) want intercepted=true, got false", tc.msg)
			}
		})
	}

	t.Run("unknown returns false", func(t *testing.T) {
		_, _, ok := m.dispatchInputMsg(struct{}{})
		if ok {
			t.Fatal("unknown msg must not be intercepted")
		}
	})
}

func TestDispatchStreamMsg(t *testing.T) {
	m := newTestModel(nil)

	cases := []tea.Msg{
		uikit.ExecWindowFetchedMsg{},
		uikit.SSEConnectedMsg{},
		uikit.SSEEventMsg{},
		uikit.SSEDisconnectedMsg{},
	}
	for _, msg := range cases {
		_, _, ok := m.dispatchStreamMsg(msg)
		if !ok {
			t.Fatalf("dispatchStreamMsg(%T) want intercepted=true", msg)
		}
	}

	if _, _, ok := m.dispatchStreamMsg(struct{}{}); ok {
		t.Fatal("unknown msg must not be intercepted")
	}
}

func TestDispatchLogMsg(t *testing.T) {
	m := newTestModel(nil)
	cases := []tea.Msg{
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
	}
	for _, msg := range cases {
		_, _, ok := m.dispatchLogMsg(msg)
		if !ok {
			t.Fatalf("dispatchLogMsg(%T) want intercepted=true", msg)
		}
	}

	if _, _, ok := m.dispatchLogMsg(struct{}{}); ok {
		t.Fatal("unknown msg must not be intercepted")
	}
}

func TestDispatchNotificationMsg(t *testing.T) {
	m := newTestModel(nil)
	cases := []tea.Msg{
		uikit.NotificationStreamConnectedMsg{},
		uikit.NotificationEventMsg{},
		uikit.NotificationStreamDisconnectedMsg{},
		uikit.NotificationUnreadCountMsg{},
		uikit.NotificationsLoadedMsg{},
		uikit.NotificationReadStateMsg{},
		uikit.NotificationBoundaryFlashClearedMsg{},
	}
	for _, msg := range cases {
		_, _, ok := m.dispatchNotificationMsg(msg)
		if !ok {
			t.Fatalf("dispatchNotificationMsg(%T) want intercepted=true", msg)
		}
	}

	if _, _, ok := m.dispatchNotificationMsg(struct{}{}); ok {
		t.Fatal("unknown msg must not be intercepted")
	}
}

func TestDispatchActionMsg(t *testing.T) {
	// Action messages call logActionResult which writes to debugView — no
	// other side-effects, so the test only needs intercepted=true.
	m := newTestModel(nil)
	cases := []tea.Msg{
		uikit.TriggerRunMsg{TaskName: "t1"},
		uikit.StopRunMsg{TaskName: "t1"},
		uikit.RestartServiceMsg{TaskName: "t1"},
		uikit.StopServiceMsg{TaskName: "t1"},
		uikit.DeleteRunMsg{TaskName: "t1"},
	}
	for _, msg := range cases {
		_, _, ok := m.dispatchActionMsg(msg)
		if !ok {
			t.Fatalf("dispatchActionMsg(%T) want intercepted=true", msg)
		}
	}

	if _, _, ok := m.dispatchActionMsg(struct{}{}); ok {
		t.Fatal("unknown msg must not be intercepted")
	}
}

func TestDispatchLifecycleMsg(t *testing.T) {
	m := newTestModel(nil)
	cases := []tea.Msg{
		uikit.TickMsg{},
		uikit.QuitMsg{Action: uikit.QuitKeepDaemon},
		uikit.FlashExpiredMsg{},
		uikit.OpenBrowserMsg{},
		uikit.OpenRunMsg{},
		uikit.SystemStatsMsg{},
		uikit.MetricsHistoryMsg{},
		uikit.RunSummaryMsg{},
	}
	for _, msg := range cases {
		_, _, ok := m.dispatchLifecycleMsg(msg)
		if !ok {
			t.Fatalf("dispatchLifecycleMsg(%T) want intercepted=true", msg)
		}
	}

	if _, _, ok := m.dispatchLifecycleMsg(struct{}{}); ok {
		t.Fatal("unknown msg must not be intercepted")
	}
}

// Update routes through the entire dispatcher chain. Verify it returns the
// model for both a recognised message and an unrecognised one (the "noop"
// fall-through at the bottom of Update).
func TestModelUpdate_RoutesAndFallsThrough(t *testing.T) {
	m := newTestModel(nil)

	// Recognised: window size routes to dispatchInputMsg.
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
	_, cmd := m.handleSystemStats(uikit.SystemStatsMsg{Err: errors.New("boom")})
	if cmd != nil {
		t.Fatal("expected nil cmd for error path")
	}

	_, cmd = m.handleSystemStats(uikit.SystemStatsMsg{Stats: &model.SystemStats{Name: "test"}})
	if cmd != nil {
		t.Fatal("expected nil cmd (handler is fire-and-forget)")
	}
}

func TestHandleMetricsHistory_ErrAndSuccess(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleMetricsHistory(uikit.MetricsHistoryMsg{Err: errors.New("x")})
	if cmd != nil {
		t.Fatal("expected nil cmd on err")
	}
	_, cmd = m.handleMetricsHistory(uikit.MetricsHistoryMsg{Samples: []model.MetricsSample{{Timestamp: 1}}})
	if cmd != nil {
		t.Fatal("expected nil cmd on success")
	}
}

func TestHandleRunSummary_ErrAndSuccess(t *testing.T) {
	m := newTestModel(nil)
	_, cmd := m.handleRunSummary(uikit.RunSummaryMsg{Err: errors.New("x")})
	if cmd != nil {
		t.Fatal("expected nil cmd on err")
	}
	_, cmd = m.handleRunSummary(uikit.RunSummaryMsg{Summary: &model.RunSummary{Total: 1}})
	if cmd != nil {
		t.Fatal("expected nil cmd on success")
	}
}

func TestHandleWindowSize_AppliesDimensions(t *testing.T) {
	m := newTestModel(nil)
	got, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 40})
	g := got.(Model)
	if g.width != 120 || g.height != 40 || !g.ready {
		t.Fatalf("handleWindowSize did not apply dimensions: %+v", g)
	}
}
