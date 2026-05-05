// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingFailureSink struct {
	mu       sync.Mutex
	captured []*Event
}

func (s *recordingFailureSink) IngestSynthetic(ev *Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captured = append(s.captured, ev)
}

func (s *recordingFailureSink) Captured() []*Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Event, len(s.captured))
	copy(out, s.captured)
	return out
}

type executeAction struct {
	id      string
	matchFn func(*Event) bool
	execFn  func(context.Context, *Event) error
	hits    atomic.Int64
}

func (a *executeAction) ID() string { return a.id }
func (a *executeAction) Match(ev *Event) bool {
	if a.matchFn != nil {
		return a.matchFn(ev)
	}
	return true
}
func (a *executeAction) Execute(ctx context.Context, ev *Event) error {
	a.hits.Add(1)
	if a.execFn != nil {
		return a.execFn(ctx, ev)
	}
	return nil
}

func TestDispatcher_DeliversMatchingActions(t *testing.T) {
	a := &executeAction{id: "a"}
	registry := NewActionRegistry([]Action{a})
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"a"}}}, registry)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, registry, 8, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)

	d.dispatch(&Event{Kind: KindRunFailed, Severity: SevError})
	d.dispatch(&Event{Kind: KindRunSucceeded, Severity: SevInfo})

	require.Eventually(t, func() bool { return a.hits.Load() == 2 }, time.Second, 10*time.Millisecond)
	d.closeQueues()
	d.waitWorkers()
}

func TestDispatcher_PermanentFailureSurfacedToSink(t *testing.T) {
	cause := errors.New("503 Service Unavailable (after retries)")
	a := &executeAction{
		id:     "slack:ops",
		execFn: func(context.Context, *Event) error { return cause },
	}
	registry := NewActionRegistry([]Action{a})
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"slack:ops"}}}, registry)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, registry, 8, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)

	d.dispatch(&Event{Kind: KindRunFailed, Severity: SevError, TaskName: "backup-db"})
	require.Eventually(t, func() bool { return len(sink.Captured()) == 1 }, time.Second, 10*time.Millisecond)

	got := sink.Captured()[0]
	assert.Equal(t, KindNotifyDeliveryFailed, got.Kind)
	assert.Equal(t, SevWarn, got.Severity)
	assert.Equal(t, "backup-db", got.TaskName)
	assert.Equal(t, cause.Error(), got.Reason)
	assert.Equal(t, "slack:ops", got.Extra["channel"])
	assert.Equal(t, "run.failed", got.Extra["original_kind"])

	d.closeQueues()
	d.waitWorkers()
}

func TestDispatcher_ContextCancelDoesNotSurfaceFailure(t *testing.T) {
	released := make(chan struct{})
	a := &executeAction{
		id: "slow",
		execFn: func(ctx context.Context, _ *Event) error {
			<-ctx.Done()
			close(released)
			return ctx.Err()
		},
	}
	registry := NewActionRegistry([]Action{a})
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"slow"}}}, registry)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, registry, 4, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	d.startWorkers(ctx)
	d.dispatch(&Event{Kind: KindRunFailed})
	// Wait for the action to enter Execute.
	require.Eventually(t, func() bool { return a.hits.Load() == 1 }, time.Second, 10*time.Millisecond)
	cancel()
	<-released
	d.closeQueues()
	d.waitWorkers()
	assert.Empty(t, sink.Captured(), "ctx cancel must not be surfaced as a delivery failure")
}

func TestDispatcher_DropsWhenQueueFull(t *testing.T) {
	block := make(chan struct{})
	a := &executeAction{
		id: "slow",
		execFn: func(ctx context.Context, _ *Event) error {
			<-block
			return nil
		},
	}
	registry := NewActionRegistry([]Action{a})
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"slow"}}}, registry)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, registry, 1, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)

	for i := 0; i < 10; i++ {
		d.dispatch(&Event{Kind: KindRunFailed})
	}
	close(block)
	d.closeQueues()
	d.waitWorkers()
	assert.Greater(t, d.DroppedActionCount(), uint64(0), "must record drops under pressure")
}
