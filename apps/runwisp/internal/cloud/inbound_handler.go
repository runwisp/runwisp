// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"context"
	"errors"
	"strings"
	"sync"

	"log/slog"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
)

// InboundHandler processes inbound WebSocket messages, encapsulating
// domain logic for execution dispatch, stop, and log operations.
type InboundHandler struct {
	taskManager     TaskRunner
	runRepo         ExternalRunGetter
	logDir          string
	availability    executor.Availability
	queueExecUpdate func(protocol.ExecutionUpdateMessage)
	uploader        *LogUploader
	tracker         *ExecutionTracker
	// requestRestart asks the daemon to restart its own process. nil (or a
	// daemon that isn't service-managed) means agent:restart is rejected.
	requestRestart func() error

	// mu guards logListeners. RWMutex because IsLogListener is on the per-log-
	// line read path (frequent), while listen/stop mutations are rare.
	mu           sync.RWMutex
	logListeners map[string]struct{}
}

// InboundHandlerDeps bundles the collaborators an InboundHandler needs.
// RequestRestart may be nil (a daemon not under a service manager), in which
// case agent:restart is rejected.
type InboundHandlerDeps struct {
	TaskManager     TaskRunner
	RunRepo         ExternalRunGetter
	LogDir          string
	Availability    executor.Availability
	QueueExecUpdate func(protocol.ExecutionUpdateMessage)
	Uploader        *LogUploader
	Tracker         *ExecutionTracker
	RequestRestart  func() error
}

func NewInboundHandler(deps InboundHandlerDeps) *InboundHandler {
	return &InboundHandler{
		taskManager:     deps.TaskManager,
		runRepo:         deps.RunRepo,
		logDir:          deps.LogDir,
		availability:    deps.Availability,
		queueExecUpdate: deps.QueueExecUpdate,
		uploader:        deps.Uploader,
		tracker:         deps.Tracker,
		requestRestart:  deps.RequestRestart,
		logListeners:    make(map[string]struct{}),
	}
}

// HandleAgentRestart asks the daemon to restart its own process. It is only
// honoured when the daemon runs under a service manager (systemd/launchd) that
// will bring it back — otherwise restarting would just stop the agent for
// good, so we reject it with a clear conflict the dashboard can surface.
func (h *InboundHandler) HandleAgentRestart() error {
	if h.requestRestart == nil {
		return &CloudError{Kind: CloudErrorKindConflict, Message: "agent is not service-managed; cannot restart remotely"}
	}
	if err := h.requestRestart(); err != nil {
		return &CloudError{Kind: CloudErrorKindConflict, Message: err.Error()}
	}
	slog.Info("agent restart requested by cloud")
	return nil
}

// LogDir returns the daemon's log directory; used by EventBridge to resolve
// per-run log file paths during terminal archival.
func (h *InboundHandler) LogDir() string { return h.logDir }

// Uploader returns the configured archival coordinator (may be nil).
func (h *InboundHandler) Uploader() *LogUploader { return h.uploader }

// HandleExecutionDispatch validates and triggers a dispatched execution.
// ack is invoked exactly once as soon as the dispatch is accepted (valid and
// either fresh or a recognized duplicate), before the run is triggered —
// the control plane uses it to stop re-dispatching.
func (h *InboundHandler) HandleExecutionDispatch(ctx context.Context, message protocol.ExecutionDispatchMessage, ack func()) error {
	executionID := strings.TrimSpace(message.Execution.ExecutionID)
	if executionID == "" {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	// Idempotent re-dispatch guard: the control plane re-publishes a dispatch
	// when its ack deadline lapses (e.g. the ack was lost in a hub restart).
	// If we already know this execution, re-ack without starting a second run;
	// if it already finished, re-queue its terminal update so the cloud can
	// reconcile a lost terminal report.
	if h.isDuplicateDispatch(ctx, executionID) {
		slog.Info("duplicate execution dispatch re-acked", "executionId", executionID)
		if ack != nil {
			ack()
		}
		return nil
	}

	if ack != nil {
		ack()
	}

	// Persist the signed PUT URL + key BEFORE we trigger the run. A crash
	// after trigger but before persistence would lose the upload metadata
	// and orphan the local log file with no way to archive it.
	if h.uploader != nil {
		if err := h.uploader.RegisterDispatch(ctx, executionID, message.Execution.LogUploadURL, message.Execution.LogPath); err != nil {
			slog.Warn("failed to register dispatch for log archival", "executionId", executionID, "err", err)
		}
	}

	taskName, configBacked, resolveErr := h.resolveDispatchTask(message.Execution)
	if resolveErr != nil {
		h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusFailed, ptr(-1), nil, nowPtr()))
		if h.uploader != nil {
			h.uploader.forget(ctx, executionID)
		}
		return resolveErr
	}

	// Only TOML-defined tasks declare params, so only they resolve InputValues.
	// An inline ad-hoc execution has no parameter declarations — forwarding its
	// InputValues would fail resolution as "unknown parameter", so ignore them.
	var inputValues map[string]string
	if configBacked {
		inputValues = message.Execution.InputValues
	}

	run, triggerErr := h.taskManager.TriggerCloudRun(taskName, executionID, inputValues)
	if triggerErr != nil {
		return h.handleTriggerError(ctx, executionID, run, triggerErr)
	}

	slog.Info("execution dispatched", "executionId", executionID, "task", taskName)
	return nil
}

// isDuplicateDispatch reports whether the daemon already knows the execution:
// actively running (tracker) or persisted as a run (repo). For terminal runs
// the stored terminal update is re-queued so a control plane that missed the
// original report converges instead of re-running the task.
func (h *InboundHandler) isDuplicateDispatch(ctx context.Context, executionID string) bool {
	if h.tracker != nil && h.tracker.IsActive(executionID) {
		return true
	}
	if h.runRepo == nil {
		return false
	}

	run, err := h.runRepo.GetRunByExternalExecutionID(ctx, executionID)
	if err != nil || run == nil {
		return false
	}
	if run.Status.IsTerminal() {
		if update := mapRunToExecutionUpdate(run); update != nil {
			h.queueExecUpdate(*update)
		}
	}
	return true
}

func (h *InboundHandler) handleTriggerError(ctx context.Context, executionID string, run *model.Run, triggerErr error) error {
	if run != nil {
		finishedAt := run.EndAt
		if finishedAt == nil {
			finishedAt = nowPtr()
		}
		h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusFailed, ptr(run.ExitCode), run.StartAt, finishedAt))
	} else {
		h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusFailed, ptr(-1), nil, nowPtr()))
	}
	if h.uploader != nil {
		h.uploader.forget(ctx, executionID)
	}
	return &CloudError{Kind: CloudErrorKindConflict, Message: triggerErr.Error()}
}

func (h *InboundHandler) HandleExecutionStop(ctx context.Context, message protocol.ExecutionStopMessage) error {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	if err := h.taskManager.TerminateRunByExternalExecutionID(executionID); err == nil {
		return nil
	}

	run, runErr := h.runRepo.GetRunByExternalExecutionID(ctx, executionID)
	if runErr != nil {
		if errors.Is(runErr, ErrNotFound) {
			return &CloudError{Kind: CloudErrorKindUnknownExecution, Message: "execution not found"}
		}
		return &CloudError{Kind: CloudErrorKindTransient, Message: "failed to inspect execution for stop", Err: runErr}
	}

	if run.Status.IsTerminal() {
		return nil
	}

	return &CloudError{Kind: CloudErrorKindConflict, Message: "execution is not currently running"}
}

// HandleLogReplayRequest reads a bounded historical page of lines and returns
// it as a single LogReplayChunkMessage. final is true when no more lines
// remain beyond this page or the run has terminated.
func (h *InboundHandler) HandleLogReplayRequest(ctx context.Context, message protocol.LogReplayRequestMessage) (protocol.LogReplayChunkMessage, error) {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return NewLogReplayChunkMessage(message.RequestID, message.ExecutionID, nil, true),
			&CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	run, err := h.runRepo.GetRunByExternalExecutionID(ctx, executionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Unknown execution ≠ end of log: a viewer can attach before the
			// dispatch reaches this daemon (row just inserted cloud-side).
			// Claiming final here made the cloud SSE handler end the stream
			// as "terminal, fully read" on a run that hadn't even started.
			// Genuinely finished runs never get here — the cloud's
			// already-terminal guard answers those from the archive.
			return NewLogReplayChunkMessage(message.RequestID, executionID, nil, false), nil
		}
		return NewLogReplayChunkMessage(message.RequestID, executionID, nil, true),
			&CloudError{Kind: CloudErrorKindTransient, Message: "failed to query execution logs", Err: err}
	}

	lines, final, readErr := readExecutionLogReplay(run, h.logDir, message.FromLine, message.Limit)
	if readErr != nil {
		return NewLogReplayChunkMessage(message.RequestID, executionID, nil, true),
			&CloudError{Kind: CloudErrorKindTransient, Message: "failed to read execution logs", Err: readErr}
	}

	return NewLogReplayChunkMessage(message.RequestID, executionID, lines, final), nil
}

// HandleLogSearchRequest greps one execution's on-disk log and returns the
// matching lines as a single LogSearchChunkMessage. Like replay, an unknown
// execution is not an error — it yields no hits with exhausted=true (nothing on
// disk to scan). Mirrors HandleLogReplayRequest's resolution and error mapping.
func (h *InboundHandler) HandleLogSearchRequest(ctx context.Context, message protocol.LogSearchRequestMessage) (protocol.LogSearchChunkMessage, error) {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return NewLogSearchChunkMessage(message.RequestID, message.ExecutionID, nil, 0, true),
			&CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	run, err := h.runRepo.GetRunByExternalExecutionID(ctx, executionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return NewLogSearchChunkMessage(message.RequestID, executionID, nil, 0, true), nil
		}
		return NewLogSearchChunkMessage(message.RequestID, executionID, nil, 0, true),
			&CloudError{Kind: CloudErrorKindTransient, Message: "failed to query execution logs", Err: err}
	}

	hits, nextLine, exhausted, searchErr := searchExecutionLog(ctx, run, h.logDir, logSearchParams{
		query:         message.Query,
		regex:         message.Regex,
		caseSensitive: message.CaseSensitive,
		limit:         message.Limit,
		fromLine:      message.FromLine,
	})
	if searchErr != nil {
		// A malformed regex is a validation error; anything else is transient.
		kind := CloudErrorKindTransient
		if ce, ok := searchErr.(*CloudError); ok {
			kind = ce.Kind
		}
		return NewLogSearchChunkMessage(message.RequestID, executionID, nil, 0, true),
			&CloudError{Kind: kind, Message: searchErr.Error()}
	}

	return NewLogSearchChunkMessage(message.RequestID, executionID, hits, nextLine, exhausted), nil
}

// HandleLogListen marks an execution for live log:line push events. Idempotent
// — repeated calls keep a single subscription. Tracker-side memory cost is
// O(set entry); the EventBridge does the actual fan-out + drop-on-full.
func (h *InboundHandler) HandleLogListen(message protocol.LogListenMessage) error {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	h.mu.Lock()
	h.logListeners[executionID] = struct{}{}
	h.mu.Unlock()

	return nil
}

func (h *InboundHandler) HandleLogStop(message protocol.LogStopMessage) {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return
	}
	h.RemoveLogListener(executionID)
}

// IsLogListener reports whether the given execution has an active subscription.
func (h *InboundHandler) IsLogListener(executionID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.logListeners[executionID]
	return ok
}

// RemoveLogListener removes a log listener (terminal-status cleanup or
// log:stop). A no-op when the listener doesn't exist.
func (h *InboundHandler) RemoveLogListener(executionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.logListeners, executionID)
}

// ClearLogListeners removes all active log listeners (e.g. on reconnect).
func (h *InboundHandler) ClearLogListeners() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logListeners = make(map[string]struct{})
}
