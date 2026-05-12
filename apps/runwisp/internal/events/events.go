// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"log/slog"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// EventType categorizes published messages.
type EventType string

const (
	EventRunCreated      EventType = "run.created"
	EventRunStarted      EventType = "run.started"
	EventRunCompleted    EventType = "run.completed"
	EventRunFailed       EventType = "run.failed"
	EventRunUpdated      EventType = "run.updated"
	EventLogLine         EventType = "log.line"
	EventLogDiskPressure EventType = "log.disk_pressure"
)

// AllEventTypes is the single source of truth for all known event types.
// SubscribeAll uses this slice, so adding a new EventType here is sufficient.
var AllEventTypes = []EventType{
	EventRunCreated,
	EventRunStarted,
	EventRunCompleted,
	EventRunFailed,
	EventRunUpdated,
	EventLogLine,
	EventLogDiskPressure,
}

// EventData is the type constraint for event payloads.
type EventData interface {
	eventData()
}

// Event is the envelope delivered to subscribers.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      EventData `json:"data"`
}

func (RunEvent) eventData()             {}
func (LogLineEvent) eventData()         {}
func (LogDiskPressureEvent) eventData() {}

// RunEvent tracks lifecycle updates for a run.
type RunEvent struct {
	Run   *model.Run `json:"run"`
	Error string     `json:"error,omitempty"`
}

// LogDiskPressureEvent fires once per run when min_free_space crosses the
// configured threshold during execution. KilledTask is true when the daemon
// also cancelled the run because its log_on_full was "kill_task". For other
// overflow policies the run keeps executing but its log writer is stopped.
type LogDiskPressureEvent struct {
	TaskName     string `json:"task_name"`
	RunID        string `json:"run_id"`
	FreeBytes    int64  `json:"free_bytes"`
	MinFreeBytes int64  `json:"min_free_bytes"`
	KilledTask   bool   `json:"killed_task"`
}

// LogLineEvent carries one log line's worth of state. Each event represents
// exactly one line on disk; LineNum is the absolute, monotonically increasing
// line index assigned by LogWriter (= rotated_lines + line_count_in_segment),
// stable across rotations.
//
// Text is the user-facing content WITHOUT trailing newline and WITHOUT any
// disk-format prefix (e.g. "[ERR] " for stderr). Subscribers that need the
// on-disk byte representation rebuild it via logutil.FormatLine(Text, Stream).
//
// Continued marks segments 2..N of a logical line that LineBuffer split when
// it exceeded MaxLineBufferSize. Each segment still gets its own LineNum.
type LogLineEvent struct {
	TaskName            string `json:"task_name"`
	RunID               string `json:"run_id"`
	ExternalExecutionID string `json:"external_execution_id,omitempty"`
	LineNum             int64  `json:"line_num"`
	Timestamp           int64  `json:"timestamp"`
	Stream              string `json:"stream"`
	Text                string `json:"text"`
	Continued           bool   `json:"continued,omitempty"`
}

// EventHandler processes events.
type EventHandler func(event Event)

// Compile-time check: *defaultEventBus satisfies EventBus.
var _ EventBus = (*defaultEventBus)(nil)

// defaultEventBus is a lightweight in-memory pub/sub hub.
type defaultEventBus struct {
	subscribers map[EventType]map[int]EventHandler
	nextID      int
	mu          sync.RWMutex
}

// NewEventBus constructs a pub/sub bus.
func NewEventBus() EventBus {
	return &defaultEventBus{
		subscribers: make(map[EventType]map[int]EventHandler),
	}
}

// Subscribe registers a handler for a specific event type.
func (eBus *defaultEventBus) Subscribe(eventType EventType, handler EventHandler) func() {
	eBus.mu.Lock()
	defer eBus.mu.Unlock()

	id := eBus.nextID
	eBus.nextID++

	if eBus.subscribers[eventType] == nil {
		eBus.subscribers[eventType] = make(map[int]EventHandler)
	}
	eBus.subscribers[eventType][id] = handler

	return func() {
		eBus.remove(eventType, id)
	}
}

// SubscribeAll registers a handler for all event types listed in AllEventTypes.
func (eBus *defaultEventBus) SubscribeAll(handler EventHandler) func() {
	var unsubscribers []func()
	for _, eventType := range AllEventTypes {
		unsubscribers = append(unsubscribers, eBus.Subscribe(eventType, handler))
	}

	return func() {
		for _, unsubscribe := range unsubscribers {
			unsubscribe()
		}
	}
}

// Publish broadcasts an event synchronously to all subscribers. Handlers are
// invoked on the caller's goroutine; panicking handlers are isolated via
// recover so one misbehaving subscriber cannot take down the caller.
//
// Callers that need async fan-out should push into their own buffered channel
// from the handler (the SSE streamer does this).
func (eBus *defaultEventBus) Publish(eventType EventType, data EventData) {
	event := Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}

	handlers := eBus.copyHandlers(eventType)
	for _, handler := range handlers {
		eBus.invoke(handler, event)
	}
}

func (eBus *defaultEventBus) invoke(handler EventHandler, event Event) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("EventBus: recovered from panic in handler", "panic", r)
		}
	}()
	handler(event)
}

func (eBus *defaultEventBus) copyHandlers(eventType EventType) []EventHandler {
	eBus.mu.RLock()
	defer eBus.mu.RUnlock()

	handlers := make([]EventHandler, 0, len(eBus.subscribers[eventType]))
	for _, handler := range eBus.subscribers[eventType] {
		handlers = append(handlers, handler)
	}
	return handlers
}

func (eBus *defaultEventBus) remove(eventType EventType, id int) {
	eBus.mu.Lock()
	defer eBus.mu.Unlock()

	if subscribers, ok := eBus.subscribers[eventType]; ok {
		delete(subscribers, id)
		if len(subscribers) == 0 {
			delete(eBus.subscribers, eventType)
		}
	}
}
