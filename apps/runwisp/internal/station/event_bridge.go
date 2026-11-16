// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// EventBridge turns runtime events into station traffic: execution updates
// into the outbox, service lifecycle changes into service:status.
type EventBridge struct {
	client        *Client
	unsubscribers []func()
}

func NewEventBridge(client *Client) *EventBridge {
	return &EventBridge{client: client}
}

// Start subscribes to the run lifecycle. ctx bounds log archival, so in-flight
// uploads abort when the daemon shuts down.
func (b *EventBridge) Start(ctx context.Context) {
	handle := func(e events.Event) { b.handleRunEvent(ctx, e) }
	bus := b.client.deps.EventBus
	b.unsubscribers = append(b.unsubscribers,
		bus.Subscribe(events.EventRunStarted, handle),
		bus.Subscribe(events.EventRunCompleted, handle),
		bus.Subscribe(events.EventRunFailed, handle),
	)
}

// Shutdown removes all event subscriptions.
func (b *EventBridge) Shutdown() {
	for _, unsubscribe := range b.unsubscribers {
		unsubscribe()
	}
	b.unsubscribers = nil
}

// handleRunEvent runs on the publisher's goroutine (the bus is synchronous),
// so the finished update is stored before the run leaves the tracker: hello's
// inventory never misses an execution in between.
func (b *EventBridge) handleRunEvent(ctx context.Context, event events.Event) {
	runEvent, ok := event.Data.(events.RunEvent)
	if !ok || runEvent.Run == nil {
		return
	}
	run := runEvent.Run

	// A service instance's run (start, exit, supervisor restart) is an
	// execution the daemon started itself, known to the station by its run id;
	// it changes the service's status too. Any other run no dispatch created
	// (a local CLI run) stays off the station.
	service := run.ExecutionID == nil
	if service {
		if !b.client.handler.isServiceRun(run) {
			return
		}
		if sess := b.client.current(); sess != nil {
			sess.serviceChanged(run.TaskName)
		}
	}
	update := executionUpdate(run)
	if update == nil {
		return
	}
	executionID := update.ExecutionID
	running := run.Status == model.PhaseRunning
	if running {
		// Tracked first: RetryPending leaves a live execution's log alone.
		b.client.tracker.TrackRunning(executionID)
	}
	if service {
		// Recorded before the update, like a dispatch's (see
		// HandleExecutionDispatch). A service runs for long, so its log always
		// streams; the station refuses the chunks when its log storage mode
		// doesn't want them.
		if err := b.client.uploader.RegisterDispatch(ctx, executionID, true); err != nil {
			slog.Warn("failed to record the service run's log for archival", "executionId", executionID, "err", err)
		}
	}
	b.client.outbox.append(update)

	if running {
		logFilePath := logutil.ResolveRunLogPath(b.client.deps.LogDir, run.TaskName, run.ID, run.CreatedAt)
		b.client.uploader.StartStream(ctx, executionID, logFilePath)
		return
	}
	b.client.tracker.Remove(executionID)
	// Off the publisher's goroutine: the upload can take a while, and the
	// runtime's execute must return to free the concurrency slot.
	go b.finalizeRun(ctx, run)
}

// finalizeRun archives a finished run's log and stores a second finished
// update carrying the archive. The station keeps the first one's outcome and
// attaches the log.
func (b *EventBridge) finalizeRun(ctx context.Context, run *model.Run) {
	defer crashguard.Guard()
	// Stop streaming before the archive removes the log file it tails.
	chunks := b.client.uploader.StopStream(ctx, executionIDOf(run))
	if archived := b.client.uploader.Archive(ctx, run, chunks); archived != nil {
		update := executionUpdate(run)
		update.Log = archived
		b.client.outbox.append(update)
	}
}
