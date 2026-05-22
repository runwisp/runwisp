// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package coalesce

import (
	"context"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoalesce_ID_DelegatesToInner(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	c := New(inner, Config{Window: time.Hour, EveryN: 10}, testutil.NewFakeClock(time.Unix(0, 0)), nil)
	defer c.Close(context.Background())
	if c.ID() != "slack-ops" {
		t.Fatalf("expected ID=slack-ops, got %q", c.ID())
	}
}

func TestCoalesce_FingerprintKey_DifferentFields(t *testing.T) {
	ev1 := &notify.Event{Kind: notify.KindRunFailed, TaskName: "a"}
	ev2 := &notify.Event{Kind: notify.KindRunFailed, TaskName: "b"}
	if fingerprintKey(ev1) == fingerprintKey(ev2) {
		t.Fatal("different task names must yield different fingerprint keys")
	}
}

// helper: build a run-failed event for taskName.
func failEvent(taskName string) *notify.Event {
	return &notify.Event{
		Kind:     notify.KindRunFailed,
		Severity: notify.SevError,
		TaskName: taskName,
	}
}

// TestCoalesce_FirstEventForwarded verifies the first event in a window is
// passed straight through.
func TestCoalesce_FirstEventForwarded(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(inner, Config{Window: time.Hour, EveryN: 10}, clock, nil)
	defer c.Close(context.Background())

	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))

	got := inner.Received()
	require.Len(t, got, 1)
	assert.Nil(t, got[0].Extra, "first delivery must not be marked as a summary")
}

// TestCoalesce_RepeatsSuppressedWithinWindow verifies the prime-directive
// fix: a flapping `* * * * *` health probe must not page Slack on every tick.
func TestCoalesce_RepeatsSuppressedWithinWindow(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(inner, Config{Window: time.Hour, EveryN: 100}, clock, nil)
	defer c.Close(context.Background())

	for i := 0; i < 5; i++ {
		require.NoError(t, c.Execute(context.Background(), failEvent("health")))
		clock.Advance(30 * time.Second)
	}
	assert.Len(t, inner.Received(), 1, "only the first event in the window should be delivered")
}

// TestCoalesce_EveryNTriggersSummary verifies the Nth suppressed event in a
// window is forwarded with coalesced_count metadata. With EveryN=3, we
// expect: ev1 forwarded, ev2/ev3 suppressed, ev4 forwarded as a summary
// (pending=3 reaches the threshold; coalesced_count counts the 3 events
// folded into this delivery — ev2 + ev3 + ev4).
func TestCoalesce_EveryNTriggersSummary(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(inner, Config{Window: time.Hour, EveryN: 3}, clock, nil)
	defer c.Close(context.Background())

	for i := 0; i < 4; i++ {
		require.NoError(t, c.Execute(context.Background(), failEvent("health")))
		clock.Advance(time.Second)
	}

	got := inner.Received()
	require.Len(t, got, 2, "first delivery + Nth coalesced summary")
	assert.Nil(t, got[0].Extra)
	assert.Equal(t, 3, got[1].Extra["coalesced_count"])
	_, hasSummaryFlag := got[1].Extra["coalesced_summary"]
	assert.False(t, hasSummaryFlag, "every-Nth dispatch is not a window-close summary")
}

// TestCoalesce_DifferentFingerprintsDoNotInterfere verifies events for
// different tasks (or different end-reasons) are coalesced independently.
func TestCoalesce_DifferentFingerprintsDoNotInterfere(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(inner, Config{Window: time.Hour, EveryN: 10}, clock, nil)
	defer c.Close(context.Background())

	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))
	require.NoError(t, c.Execute(context.Background(), failEvent("backup")))
	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))    // suppressed
	require.NoError(t, c.Execute(context.Background(), failEvent("backup"))) // suppressed

	assert.Len(t, inner.Received(), 2, "each fingerprint gets its own first delivery")
}

// TestCoalesce_NewWindowAfterExpiry verifies that once the window has
// elapsed, a fresh event is forwarded immediately again (not as a summary).
func TestCoalesce_NewWindowAfterExpiry(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(inner, Config{Window: time.Hour, EveryN: 100}, clock, nil)
	defer c.Close(context.Background())

	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))
	clock.Advance(time.Hour + time.Second)
	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))

	got := inner.Received()
	require.Len(t, got, 2)
	assert.Nil(t, got[1].Extra, "post-window delivery is a fresh first, not a summary")
}

// TestCoalesce_WindowCloseSummary verifies the timer-driven flush: when a
// window expires while pending events are buffered, a summary fires
// asynchronously. Uses a real-time short window so the timer actually runs.
func TestCoalesce_WindowCloseSummary(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	c := New(inner, Config{Window: 50 * time.Millisecond, EveryN: 1000}, nil, nil)
	defer c.Close(context.Background())

	for i := 0; i < 4; i++ {
		require.NoError(t, c.Execute(context.Background(), failEvent("health")))
	}

	// Wait for the window-close timer to fire and the summary delivery to
	// complete. Allow generous slack for slow CI.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(inner.Received()) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := inner.Received()
	require.GreaterOrEqual(t, len(got), 2, "expected first delivery + window-close summary")
	summary := got[len(got)-1]
	require.NotNil(t, summary.Extra)
	assert.Equal(t, true, summary.Extra["coalesced_summary"])
	count, _ := summary.Extra["coalesced_count"].(int)
	assert.GreaterOrEqual(t, count, 2, "summary count must include suppressed events")
}

// TestCoalesce_CloseStopsTimers verifies Close cancels pending timers without
// leaking goroutines or causing further deliveries after shutdown.
func TestCoalesce_CloseStopsTimers(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	c := New(inner, Config{Window: time.Hour, EveryN: 1000}, nil, nil)

	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))
	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))

	require.NoError(t, c.Close(context.Background()))
	assert.True(t, inner.Closed(), "inner channel must be closed")

	// Give any leaked timers a chance to misfire — we only expect the first
	// pre-close delivery in the count.
	time.Sleep(20 * time.Millisecond)
	assert.Len(t, inner.Received(), 1)
}

func TestFingerprintKey_NilEvent(t *testing.T) {
	assert.Equal(t, "", fingerprintKey(nil))
}

func TestFingerprintKey_WithEndReason(t *testing.T) {
	r := sqlcdb.ReasonFailed
	ev := &notify.Event{
		Kind:     notify.KindRunFailed,
		TaskName: "t1",
		Run:      &sqlcdb.Run{EndReason: &r},
	}
	key := fingerprintKey(ev)
	assert.Contains(t, key, "failed")
}

func TestFingerprintKey_WithDeliveryFailed(t *testing.T) {
	ev := &notify.Event{
		Kind:     notify.KindNotifyDeliveryFailed,
		TaskName: "t1",
		Extra:    map[string]any{"channel": "slack", "original_kind": "run.failed"},
	}
	key := fingerprintKey(ev)
	assert.Contains(t, key, "slack")
	assert.Contains(t, key, "run.failed")
}

func TestSummarize_NonNilExtra(t *testing.T) {
	ev := &notify.Event{
		Kind:  notify.KindRunFailed,
		Extra: map[string]any{"foo": "bar"},
	}
	out := summarize(ev, 5, false)
	assert.Equal(t, "bar", out.Extra["foo"], "existing extra keys preserved")
	assert.Equal(t, 5, out.Extra["coalesced_count"])
	_, hasFlag := out.Extra["coalesced_summary"]
	assert.False(t, hasFlag)
}

func TestTimerFlush_EmptyState(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	c := New(inner, Config{Window: time.Hour, EveryN: 1}, nil, nil)
	defer c.Close(context.Background())
	// calling timerFlush with a key that has no state is a no-op
	c.timerFlush("nonexistent-key")
	assert.Empty(t, inner.Received())
}
