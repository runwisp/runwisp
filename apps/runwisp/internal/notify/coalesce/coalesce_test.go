// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package coalesce

import (
	"context"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
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

// withManualTimers swaps the channel's afterFunc seam for a deterministic
// ManualTimers, so window-close flushes fire on demand instead of on the wall
// clock. Returns the timers handle for the test to drive.
func withManualTimers(c *Channel) *testutil.ManualTimers {
	mt := testutil.NewManualTimers()
	c.after = func(_ time.Duration, fn func()) timerStopper { return mt.After(0, fn) }
	return mt
}

// TestCoalesce_WindowCloseSummary verifies the timer-driven flush: when a
// window expires while pending events are buffered, a summary fires. The timer
// is driven manually so the assertion is deterministic.
func TestCoalesce_WindowCloseSummary(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	clock := testutil.NewFakeClock(time.Unix(0, 0))
	c := New(inner, Config{Window: time.Hour, EveryN: 1000}, clock, nil)
	defer c.Close(context.Background())
	mt := withManualTimers(c)

	for i := 0; i < 4; i++ {
		require.NoError(t, c.Execute(context.Background(), failEvent("health")))
	}
	require.Equal(t, 1, mt.Pending(), "a single window-close timer should be armed")

	// Fire the window-close timer, then wait for the (tracked) async summary
	// delivery to complete — no sleep, no polling.
	mt.FireAll()
	c.wg.Wait()

	got := inner.Received()
	require.Len(t, got, 2, "expected first delivery + window-close summary")
	summary := got[len(got)-1]
	require.NotNil(t, summary.Extra)
	assert.Equal(t, true, summary.Extra["coalesced_summary"])
	count, _ := summary.Extra["coalesced_count"].(int)
	assert.Equal(t, 3, count, "summary count must include all suppressed events")
}

// TestCoalesce_CloseStopsTimers verifies Close cancels pending timers without
// leaking goroutines or causing further deliveries after shutdown.
func TestCoalesce_CloseStopsTimers(t *testing.T) {
	inner := testutil.NewFakeChannel("slack-ops")
	c := New(inner, Config{Window: time.Hour, EveryN: 1000}, testutil.NewFakeClock(time.Unix(0, 0)), nil)
	mt := withManualTimers(c)

	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))
	require.NoError(t, c.Execute(context.Background(), failEvent("etl")))
	require.Equal(t, 1, mt.Pending(), "the suppressed second event arms a timer")

	require.NoError(t, c.Close(context.Background()))
	assert.True(t, inner.Closed(), "inner channel must be closed")
	assert.Equal(t, 0, mt.Pending(), "Close must stop the pending timer")

	// Even if a stopped timer somehow fired, the timerDone guard must suppress
	// any post-close delivery. Firing manually proves it deterministically.
	mt.FireAll()
	c.wg.Wait()
	assert.Len(t, inner.Received(), 1)
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
