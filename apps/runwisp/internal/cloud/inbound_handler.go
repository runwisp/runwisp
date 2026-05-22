// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
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

	mu           sync.Mutex
	logListeners map[string]struct{}
}

func NewInboundHandler(
	taskManager TaskRunner,
	runRepo ExternalRunGetter,
	logDir string,
	availability executor.Availability,
	queueExecUpdate func(protocol.ExecutionUpdateMessage),
	uploader *LogUploader,
) *InboundHandler {
	return &InboundHandler{
		taskManager:     taskManager,
		runRepo:         runRepo,
		logDir:          logDir,
		availability:    availability,
		queueExecUpdate: queueExecUpdate,
		uploader:        uploader,
		logListeners:    make(map[string]struct{}),
	}
}

// LogDir returns the daemon's log directory; used by EventBridge to resolve
// per-run log file paths during terminal archival.
func (h *InboundHandler) LogDir() string { return h.logDir }

// Uploader returns the configured archival coordinator (may be nil).
func (h *InboundHandler) Uploader() *LogUploader { return h.uploader }

func (h *InboundHandler) HandleExecutionDispatch(message protocol.ExecutionDispatchMessage) error {
	executionID := strings.TrimSpace(message.Execution.ExecutionID)
	if executionID == "" {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	// Persist the signed PUT URL + key BEFORE we trigger the run. A crash
	// after trigger but before persistence would lose the upload metadata
	// and orphan the local log file with no way to archive it.
	if h.uploader != nil {
		if err := h.uploader.RegisterDispatch(executionID, message.Execution.LogUploadURL, message.Execution.LogPath); err != nil {
			slog.Warn("failed to register dispatch for log archival", "executionId", executionID, "err", err)
		}
	}

	taskName, resolveErr := h.resolveDispatchTask(message.Execution)
	if resolveErr != nil {
		h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusErr, ptr(-1), nil, nowPtr()))
		if h.uploader != nil {
			h.uploader.Forget(executionID)
		}
		return resolveErr
	}

	run, triggerErr := h.taskManager.TriggerCloudRun(taskName, executionID)
	if triggerErr != nil {
		return h.handleTriggerError(executionID, run, triggerErr)
	}

	slog.Info("execution dispatched", "executionId", executionID, "task", taskName)
	return nil
}

func (h *InboundHandler) handleTriggerError(executionID string, run *model.Run, triggerErr error) error {
	if run != nil {
		finishedAt := run.EndAt
		if finishedAt == nil {
			finishedAt = nowPtr()
		}
		h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusErr, ptr(run.ExitCode), run.StartAt, finishedAt))
	} else {
		h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusErr, ptr(-1), nil, nowPtr()))
	}
	if h.uploader != nil {
		h.uploader.Forget(executionID)
	}
	return &CloudError{Kind: CloudErrorKindConflict, Message: triggerErr.Error()}
}

func (h *InboundHandler) HandleExecutionStop(message protocol.ExecutionStopMessage) error {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	if err := h.taskManager.TerminateRunByExternalExecutionID(executionID); err == nil {
		return nil
	}

	run, runErr := h.runRepo.GetRunByExternalExecutionID(executionID)
	if runErr != nil {
		if errors.Is(runErr, ErrNotFound) {
			return &CloudError{Kind: CloudErrorKindConflict, Message: "execution not found"}
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
func (h *InboundHandler) HandleLogReplayRequest(message protocol.LogReplayRequestMessage) (protocol.LogReplayChunkMessage, error) {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return NewLogReplayChunkMessage(message.ID, message.ExecutionID, nil, true),
			&CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	run, err := h.runRepo.GetRunByExternalExecutionID(executionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return NewLogReplayChunkMessage(message.ID, executionID, nil, true), nil
		}
		return NewLogReplayChunkMessage(message.ID, executionID, nil, true),
			&CloudError{Kind: CloudErrorKindTransient, Message: "failed to query execution logs", Err: err}
	}

	lines, final, readErr := readExecutionLogReplay(run, h.logDir, message.FromLine, message.Limit)
	if readErr != nil {
		return NewLogReplayChunkMessage(message.ID, executionID, nil, true),
			&CloudError{Kind: CloudErrorKindTransient, Message: "failed to read execution logs", Err: readErr}
	}

	return NewLogReplayChunkMessage(message.ID, executionID, lines, final), nil
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
	h.mu.Lock()
	delete(h.logListeners, executionID)
	h.mu.Unlock()
}

// IsLogListener reports whether the given execution has an active subscription.
func (h *InboundHandler) IsLogListener(executionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.logListeners[executionID]
	return ok
}

// RemoveLogListener removes a log listener (terminal-status cleanup or
// log:stop). Returns true if the listener existed.
func (h *InboundHandler) RemoveLogListener(executionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.logListeners[executionID]; !ok {
		return false
	}
	delete(h.logListeners, executionID)
	return true
}

// ClearLogListeners removes all active log listeners (e.g. on reconnect).
func (h *InboundHandler) ClearLogListeners() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logListeners = make(map[string]struct{})
}
