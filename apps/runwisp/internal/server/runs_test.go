// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify/channel/inapp"
	"github.com/stretchr/testify/assert"
)

func TestToSSEEventData_RunDeletedHappyPath(t *testing.T) {
	in := events.Event{
		Type: events.EventRunDeleted,
		Data: events.RunDeletedEvent{RunID: "r-1", TaskName: "t"},
	}
	got := toSSEEventData(in)
	d, ok := got.(RunDeletedSSEEvent)
	if !ok {
		t.Fatalf("type = %T, want RunDeletedSSEEvent", got)
	}
	assert.Equal(t, "r-1", d.RunID)
	assert.Equal(t, "t", d.TaskName)
}

func TestToSSEEventData_RunDeletedMismatchedDataReturnsEmpty(t *testing.T) {
	// Wrong concrete type in Data — fallback to zero RunDeletedSSEEvent.
	in := events.Event{Type: events.EventRunDeleted, Data: events.RunEvent{}}
	got := toSSEEventData(in)
	d, ok := got.(RunDeletedSSEEvent)
	if !ok {
		t.Fatalf("type = %T, want RunDeletedSSEEvent", got)
	}
	assert.Equal(t, "", d.RunID)
}

func TestToSSEEventData_NonRunEventDataFallsBackToUpdated(t *testing.T) {
	// For non-deleted types, if Data isn't RunEvent we return zero RunUpdatedEvent.
	in := events.Event{Type: events.EventRunCreated, Data: events.RunDeletedEvent{}}
	got := toSSEEventData(in)
	if _, ok := got.(RunUpdatedEvent); !ok {
		t.Fatalf("type = %T, want RunUpdatedEvent", got)
	}
}

func TestToSSEEventData_DispatchesByEventType(t *testing.T) {
	re := events.RunEvent{Run: &model.Run{ID: "r-9"}}
	cases := []struct {
		name string
		typ  events.EventType
		// match func: returns true when got is the expected wrapper
		want func(any) bool
	}{
		{"created", events.EventRunCreated, func(v any) bool { _, ok := v.(RunCreatedEvent); return ok }},
		{"started", events.EventRunStarted, func(v any) bool { _, ok := v.(RunStartedEvent); return ok }},
		{"completed", events.EventRunCompleted, func(v any) bool { _, ok := v.(RunCompletedEvent); return ok }},
		{"failed", events.EventRunFailed, func(v any) bool { _, ok := v.(RunFailedEvent); return ok }},
		{"updated", events.EventRunUpdated, func(v any) bool { _, ok := v.(RunUpdatedEvent); return ok }},
		// Unknown event type with valid RunEvent data falls through to default → RunUpdatedEvent.
		{"unknown_default", events.EventType("run.bogus"), func(v any) bool { _, ok := v.(RunUpdatedEvent); return ok }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toSSEEventData(events.Event{Type: tc.typ, Data: re})
			if !tc.want(got) {
				t.Fatalf("type = %T did not match expected wrapper", got)
			}
		})
	}
}

// Regression: appStreamHandler must subscribe to the notify hub before
// flushing the appEvents replay backlog, so a notification published while
// that flush is still in progress queues in the subscriber's own buffered
// channel instead of reaching zero subscribers and vanishing. This drives the
// handler directly with a send() that deterministically blocks partway
// through the replay loop, publishes to the hub while blocked there, and
// confirms the update still reaches the stream once the loop resumes.
func TestAppStreamHandler_NotifyHubSubscribedBeforeReplayFlush(t *testing.T) {
	s, _, _, _ := setupServer(t)
	hub := inapp.NewHub(0)
	s.notifyHub = hub

	// Two ring events give the replay loop below a non-empty backlog: an
	// empty backlog (a fresh, non-reconnecting client) completes instantly and
	// never gives a concurrent publish a window to land in.
	s.eventBus.Publish(events.EventRunCreated, events.RunEvent{Run: &model.Run{ID: ulid.Make().String()}})
	s.eventBus.Publish(events.EventRunCreated, events.RunEvent{Run: &model.Run{ID: ulid.Make().String()}})

	var mu sync.Mutex
	var messages []sse.Message
	replayBlocked := make(chan struct{})
	release := make(chan struct{})
	callCount := 0
	fakeSend := func(msg sse.Message) error {
		mu.Lock()
		messages = append(messages, msg)
		callCount++
		n := callCount
		mu.Unlock()
		if n == 2 { // call 1 = initial ping, call 2 = the first (only) replay event
			close(replayBlocked)
			<-release
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.appStreamHandler(ctx, &AppStreamInput{LastEventID: "1"}, fakeSend)
		close(done)
	}()

	<-replayBlocked
	// If the hub was already subscribed at this point (the fix), this queues
	// in the subscriber's buffered channel. Before the fix, notifyHub.Subscribe
	// only ran after this whole replay loop returned, so the publish would find
	// zero subscribers and be dropped for good.
	hub.Publish(inapp.Update{Type: inapp.UpdateTypeUnreadCountChanged, UnreadCount: 7})
	close(release)

	// Give pumpAppStream a beat to drain the queued update, then end the stream.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	for _, m := range messages {
		if _, ok := m.Data.(NotificationUnreadCountEvent); ok {
			return
		}
	}
	t.Fatalf("notification published during the replay flush never reached the stream; messages: %+v", messages)
}
