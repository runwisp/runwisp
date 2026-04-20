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
	EventRunCreated   EventType = "run.created"
	EventRunStarted   EventType = "run.started"
	EventRunCompleted EventType = "run.completed"
	EventRunFailed    EventType = "run.failed"
	EventRunUpdated   EventType = "run.updated"
	EventLogLine      EventType = "log.line"
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

func (RunEvent) eventData()     {}
func (LogLineEvent) eventData() {}

// RunEvent tracks lifecycle updates for a run.
type RunEvent struct {
	Run   *model.Run `json:"run"`
	Error string     `json:"error,omitempty"`
}

// LogLineEvent streams stdout/stderr updates.
type LogLineEvent struct {
	TaskName            string `json:"task_name"`
	RunID               string `json:"run_id"`
	ExternalExecutionID string `json:"external_execution_id,omitempty"`
	Line                string `json:"line"`
	Stream              string `json:"stream"`
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

// Publish broadcasts an event asynchronously.
func (eBus *defaultEventBus) Publish(eventType EventType, data EventData) {
	event := Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}

	handlers := eBus.copyHandlers(eventType)
	for _, handler := range handlers {
		h := handler // capture handler for goroutine
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("EventBus: recovered from panic in handler", "panic", r)
				}
			}()
			h(event)
		}()
	}
}

// PublishSync broadcasts an event synchronously.
func (eBus *defaultEventBus) PublishSync(eventType EventType, data EventData) {
	event := Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}

	handlers := eBus.copyHandlers(eventType)
	for _, handler := range handlers {
		handler(event)
	}
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
