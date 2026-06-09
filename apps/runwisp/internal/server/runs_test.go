// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"testing"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
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
