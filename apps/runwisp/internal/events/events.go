// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	EventRunDeleted      EventType = "run.deleted"
	EventLogLine         EventType = "log.line"
	EventLogRegion       EventType = "log.region"
	EventLogDiskPressure EventType = "log.disk_pressure"
	EventServiceFatal    EventType = "service.fatal"
	EventSystemSample    EventType = "system"
	EventConfigStale     EventType = "config.stale"
)

// AllEventTypes lists the run/log lifecycle events SubscribeAll fans out to.
// EventSystemSample and EventConfigStale are deliberately excluded: they are
// periodic UI-push events, not lifecycle events, so notify (a SubscribeAll
// consumer) need not churn on them. Subscribers that want them attach directly
// with Subscribe(EventSystemSample, …).
var AllEventTypes = []EventType{
	EventRunCreated,
	EventRunStarted,
	EventRunCompleted,
	EventRunFailed,
	EventRunUpdated,
	EventRunDeleted,
	EventLogLine,
	EventLogRegion,
	EventLogDiskPressure,
	EventServiceFatal,
}

// EventData is a sealed-union marker; the eventData() method exists only to constrain
// the type set. The single-method interface is intentional — it is not a behavioural abstraction.
type EventData interface { //NOSONAR: sealed-type discriminant, not a behavioural interface
	eventData()
}

// Event is the envelope delivered to subscribers.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      EventData `json:"data"`
}

// eventData is a marker method; the empty body is intentional — it exists only to constrain the EventData type set.
func (RunEvent) eventData()             { /* sealed-type marker */ }
func (RunDeletedEvent) eventData()      { /* sealed-type marker */ }
func (LogLineEvent) eventData()         { /* sealed-type marker */ }
func (LogRegionEvent) eventData()       { /* sealed-type marker */ }
func (LogDiskPressureEvent) eventData() { /* sealed-type marker */ }
func (ServiceFatalEvent) eventData()    { /* sealed-type marker */ }
func (SystemSampleEvent) eventData()    { /* sealed-type marker */ }
func (ConfigStaleEvent) eventData()     { /* sealed-type marker */ }

// SystemSampleEvent carries a periodic system resource snapshot pushed to live
// dashboards so they don't poll /api/system. Uptime is formatted server-side
// from the daemon start time; the sample is the same shape the metrics history
// endpoint serves, so a viewer can append it straight onto the chart.
type SystemSampleEvent struct {
	Sample model.MetricsSample
	Uptime string
}

// ConfigStaleEvent fires only when the daemon's config-staleness flips: a TOML
// edit lands un-applied (true) or a reload clears it (false). Dashboards drive
// the "restart to apply" banner off this instead of polling /api/info.
type ConfigStaleEvent struct {
	Stale bool
}

// RunEvent tracks lifecycle updates for a run.
//
// LogPath is the on-disk log file resolved by the executor when the run
// starts. It is not persisted on the runs row — subscribers (cloud, notify)
// that need the path read it from the event envelope rather than the Run
// itself. Empty for events fired before the executor has resolved the path.
type RunEvent struct {
	Run     *model.Run `json:"run"`
	LogPath string     `json:"log_path,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// RunDeletedEvent fires when a run row has been soft-deleted. The slim envelope
// is intentional: subscribers (notably the SSE-driven UI cache) only need the
// identity to splice the row out — they already have the full Run from prior
// events.
type RunDeletedEvent struct {
	RunID    string `json:"run_id"`
	TaskName string `json:"task_name"`
}

// ServiceFatalEvent fires when a service instance exhausts its start_retries
// budget and the supervisor gives up restarting it. It carries enough context
// to alert loudly (notify maps it to a SevError in-app bell + global
// notifiers) without the subscriber needing to load the run row. Attempts is
// the consecutive fast-failure count that tripped FATAL; LastExitCode is the
// exit code of the final failed run.
type ServiceFatalEvent struct {
	TaskName      string `json:"task_name"`
	InstanceIndex int    `json:"instance_index"`
	Attempts      int    `json:"attempts"`
	LastExitCode  int    `json:"last_exit_code"`
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
//
// FrameCount is non-zero only on the anchor line of a settled progress bar or
// multi-line redraw: it reports how many prior whole-region frames were recorded
// for that line in the `.fhist` sidecar, so a viewer knows the line is clickable
// to rewind. Plain output carries 0.
type LogLineEvent struct {
	TaskName            string `json:"task_name"`
	RunID               string `json:"run_id"`
	ExternalExecutionID string `json:"external_execution_id,omitempty"`
	LineNum             int64  `json:"line_num"`
	Timestamp           int64  `json:"timestamp"`
	Stream              string `json:"stream"`
	Text                string `json:"text"`
	Continued           bool   `json:"continued,omitempty"`
	FrameCount          int    `json:"frame_count,omitempty"`
}

// LogRegionEvent carries a snapshot of a still-animating output region (a `\r`
// progress bar or a multi-line ANSI redraw) for live viewers only. It is never
// persisted: the durable log receives the finalized frame as ordinary
// LogLineEvents when the region settles.
//
// Rows is the full current frame of the region in row order; subscribers
// REPLACE their overlay for (Stream, Epoch) wholesale on each event rather than
// merging — the next frame always supersedes the previous one, so a dropped
// snapshot self-heals. Epoch bumps whenever the region resets (clear screen,
// alt-screen, scroll-off), so a late snapshot from an old epoch can be
// discarded instead of painting over the current region.
//
// These events bypass line-number dedupe entirely (they carry no LineNum) and
// are droppable under SSE backpressure without counting as a gap.
type LogRegionEvent struct {
	TaskName            string   `json:"task_name"`
	RunID               string   `json:"run_id"`
	ExternalExecutionID string   `json:"external_execution_id,omitempty"`
	Timestamp           int64    `json:"timestamp"`
	Stream              string   `json:"stream"`
	Epoch               int      `json:"epoch"`
	Rows                []string `json:"rows"`
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
