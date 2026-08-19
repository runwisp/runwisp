// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"context"
	"sync"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

const (
	// logBatchWindow is how long live log lines are coalesced before the batch
	// is flushed as one log:lines frame. Sub-100ms keeps the live tail feeling
	// instant while collapsing a burst into a single frame/publish/SSE write.
	logBatchWindow = 50 * time.Millisecond
	// logBatchMaxLines caps a single batch so a firehose run flushes on size
	// (bounding frame memory) instead of waiting for the window.
	logBatchMaxLines = 128
)

// EventBridge subscribes to runtime events and forwards execution status
// and log lines to the cloud connection.
type EventBridge struct {
	eventBus      EventSubscriber
	handler       *InboundHandler
	tracker       *ExecutionTracker
	sendReady     func(any) error
	unsubscribers []func()

	// pendingLogs coalesces live log lines per execution between flushes.
	batchMu     sync.Mutex
	pendingLogs map[string][]protocol.LinesItem
}

func NewEventBridge(
	eventBus EventSubscriber,
	handler *InboundHandler,
	tracker *ExecutionTracker,
	sendReady func(any) error,
) *EventBridge {
	return &EventBridge{
		eventBus:    eventBus,
		handler:     handler,
		tracker:     tracker,
		sendReady:   sendReady,
		pendingLogs: make(map[string][]protocol.LinesItem),
	}
}

// Start registers event listeners on the event bus. ctx is threaded into log
// archival so in-flight uploads abort when the daemon shuts down.
func (b *EventBridge) Start(ctx context.Context) {
	b.unsubscribers = append(b.unsubscribers,
		b.eventBus.Subscribe(events.EventRunStarted, func(e events.Event) { b.handleRunEvent(ctx, e) }),
		b.eventBus.Subscribe(events.EventRunCompleted, func(e events.Event) { b.handleRunEvent(ctx, e) }),
		b.eventBus.Subscribe(events.EventRunFailed, func(e events.Event) { b.handleRunEvent(ctx, e) }),
		b.eventBus.Subscribe(events.EventLogLine, b.handleLogLineEvent),
	)
	go b.flushLogBatchesLoop(ctx)
	// A service emits a status snapshot only on an instance lifecycle change, so a
	// stable always-on service falls silent for as long as it keeps running — long
	// past the control plane's snapshot TTL, which then shows the service as having
	// no live status. Re-push all snapshots on a ticker so the view stays fresh;
	// sendReady is a no-op while no session is attached.
	go b.resendServiceStatusLoop(ctx)
}

// resendServiceStatusLoop periodically re-pushes every service's supervisor
// snapshot until ctx is cancelled (daemon shutdown).
func (b *EventBridge) resendServiceStatusLoop(ctx context.Context) {
	ticker := time.NewTicker(serviceStatusResendInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.EmitAllServiceStatus()
		}
	}
}

// Shutdown removes all event subscriptions.
func (b *EventBridge) Shutdown() {
	for _, unsubscribe := range b.unsubscribers {
		if unsubscribe != nil {
			unsubscribe()
		}
	}
	b.unsubscribers = nil
}

func (b *EventBridge) handleRunEvent(ctx context.Context, event events.Event) {
	runEvent, ok := event.Data.(events.RunEvent)
	if !ok || runEvent.Run == nil {
		return
	}

	run := runEvent.Run

	// Service-instance lifecycle changes (start / exit / supervisor restart)
	// carry no cloud execution id: they refresh the service's status snapshot
	// rather than flip an execution row. Cloud keeps only the latest snapshot
	// (no per-instance rows), mirroring the system-stats heartbeat pattern.
	// Non-service local runs return ok=false from ServiceSnapshot and no-op.
	if run.ExecutionID == nil {
		b.emitServiceStatus(run.TaskName)
		return
	}
	executionID := *run.ExecutionID
	update := mapRunToExecutionUpdate(run)
	if update == nil {
		return
	}

	terminal := run.Status.IsTerminal()

	if run.Status == model.PhaseRunning {
		b.tracker.TrackRunning(executionID, run.StartedAt)
	}
	if terminal {
		b.tracker.Remove(executionID)
	}

	if !terminal {
		b.tracker.QueueUpdate(*update, b.sendReady)
		return
	}

	// Terminal path (off the publishing goroutine so the runtime's `execute`
	// returns promptly and frees the concurrency slot): report the terminal
	// state immediately, then archive and late-attach the coordinates.
	go b.finalizeRun(ctx, run, *update, executionID)
}

// finalizeRun sends the terminal update immediately (no logPath) so the
// cloud flips the row the moment the process exits, then runs the archive
// upload (gzip + signed PUT, up to 90s) and queues a second identical
// terminal update carrying logPath/logSize. The second update loses the
// cloud's guarded terminal race by design; the statewriter late-attaches
// its archive coordinates to the already-terminal row.
func (b *EventBridge) finalizeRun(ctx context.Context, run *model.Run, update protocol.ExecutionUpdateMessage, executionID string) {
	b.tracker.QueueUpdate(update, b.sendReady)

	uploader := b.handler.Uploader()
	if uploader != nil {
		logFilePath := logutil.ResolveRunLogPath(b.handler.LogDir(), run.TaskName, run.ID, run.CreatedAt)
		result, err := uploader.Archive(ctx, executionID, logFilePath)
		switch {
		case err != nil:
			slog.Warn("log archival failed; archive coordinates not reported", "executionId", executionID, "err", err)
		case result != nil:
			update.LogPath = result.LogPath
			update.LogSize = result.LogSize
			b.tracker.QueueUpdate(update, b.sendReady)
		}
	}

	b.handler.RemoveLogListener(executionID)
}

// EmitAllServiceStatus pushes the current supervisor snapshot for every
// registered service. Called on each (re)connect (for an immediate refresh) and
// on the resend ticker, so the control plane's view survives its snapshot TTL
// for a service that produces no lifecycle events. Best-effort like
// emitServiceStatus: sendReady drops while no session is attached.
func (b *EventBridge) EmitAllServiceStatus() {
	if b.handler.taskManager == nil {
		return
	}
	for _, svc := range b.handler.taskManager.ListServiceTasks() {
		if svc == nil {
			continue
		}
		b.emitServiceStatus(svc.Name)
	}
}

// emitServiceStatus pushes the current supervisor snapshot for a service task
// to the cloud. A no-op for unknown / non-service tasks. Best-effort: a full
// outbound queue drops the snapshot rather than blocking the supervisor — the
// next lifecycle change or resend-ticker tick corrects the view.
func (b *EventBridge) emitServiceStatus(taskName string) {
	if b.handler.taskManager == nil {
		return
	}
	snapshot, ok := b.handler.taskManager.ServiceSnapshot(taskName)
	if !ok {
		return
	}
	_ = b.sendReady(NewServiceStatusMessage(snapshot))
}

func (b *EventBridge) handleLogLineEvent(event events.Event) {
	logEvent, ok := event.Data.(events.LogLineEvent)
	if !ok || logEvent.ExecutionID == "" {
		return
	}
	if !b.handler.IsLogListener(logEvent.ExecutionID) {
		return
	}
	stream := linesItemStreamFromString(logEvent.Stream)
	item := protocol.LinesItem{
		N:         logEvent.LineNum,
		Ts:        logEvent.Timestamp,
		Stream:    &stream,
		Text:      logEvent.Text,
		Continued: logEvent.Continued,
	}
	execID := logEvent.ExecutionID

	var full []protocol.LinesItem
	b.batchMu.Lock()
	b.pendingLogs[execID] = append(b.pendingLogs[execID], item)
	if len(b.pendingLogs[execID]) >= logBatchMaxLines {
		full = b.pendingLogs[execID]
		delete(b.pendingLogs, execID)
	}
	b.batchMu.Unlock()

	// Flush on a full batch immediately (bounds frame size under a firehose);
	// otherwise the window loop ships it within logBatchWindow.
	if full != nil {
		b.sendLogBatch(execID, full)
	}
}

// flushLogBatchesLoop ships coalesced log batches every logBatchWindow until
// the session context is cancelled. Mirrors the heartbeat/watchdog loop shape.
func (b *EventBridge) flushLogBatchesLoop(ctx context.Context) {
	ticker := time.NewTicker(logBatchWindow)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.flushLogBatches()
		}
	}
}

// flushLogBatches drains every pending per-execution buffer into one log:lines
// frame each. Snapshots under the lock, sends after releasing it so a slow
// send never blocks the executor goroutine appending new lines.
func (b *EventBridge) flushLogBatches() {
	b.batchMu.Lock()
	if len(b.pendingLogs) == 0 {
		b.batchMu.Unlock()
		return
	}
	batches := b.pendingLogs
	b.pendingLogs = make(map[string][]protocol.LinesItem)
	b.batchMu.Unlock()

	for execID, lines := range batches {
		b.sendLogBatch(execID, lines)
	}
}

// sendLogBatch pushes one coalesced frame. Best-effort like the old per-line
// path: a full outbound queue drops the batch rather than blocking the run —
// the viewer backfills the gap via log:replayRequest. Never a stalled run.
func (b *EventBridge) sendLogBatch(execID string, lines []protocol.LinesItem) {
	_ = b.sendReady(NewLogLinesMessage(execID, lines))
}
