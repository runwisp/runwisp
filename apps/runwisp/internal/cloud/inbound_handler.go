// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
	"log/slog"
)

type logListener struct {
	Offset int64
}

// InboundHandler processes inbound WebSocket messages, encapsulating
// domain logic for execution dispatch, stop, and log operations.
type InboundHandler struct {
	taskManager     runtime.TaskRunner
	runRepo         storage.RunRepository
	logDir          string
	availability    executor.Availability
	queueExecUpdate func(protocol.ExecutionUpdateMessage)

	mu           sync.Mutex
	logListeners map[string]*logListener
}

func NewInboundHandler(
	taskManager runtime.TaskRunner,
	runRepo storage.RunRepository,
	logDir string,
	availability executor.Availability,
	queueExecUpdate func(protocol.ExecutionUpdateMessage),
) *InboundHandler {
	return &InboundHandler{
		taskManager:     taskManager,
		runRepo:         runRepo,
		logDir:          logDir,
		availability:    availability,
		queueExecUpdate: queueExecUpdate,
		logListeners:    make(map[string]*logListener),
	}
}

func (h *InboundHandler) HandleExecutionDispatch(message protocol.ExecutionDispatchMessage) error {
	executionID := strings.TrimSpace(message.Execution.ExecutionID)
	if executionID == "" {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	taskName, resolveErr := h.resolveDispatchTask(message.Execution)
	if resolveErr != nil {
		h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusErr, ptr(-1), nil, nowPtr()))
		return resolveErr
	}

	run, triggerErr := h.taskManager.TriggerRunWithOptions(taskName, runtimeTriggerOptions(executionID))
	if triggerErr != nil {
		if run != nil {
			finishedAt := run.EndAt
			if finishedAt == nil {
				finishedAt = nowPtr()
			}
			h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusErr, ptr(run.ExitCode), run.StartAt, finishedAt))
		} else {
			h.queueExecUpdate(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusErr, ptr(-1), nil, nowPtr()))
		}
		return &CloudError{Kind: CloudErrorKindConflict, Message: triggerErr.Error()}
	}

	slog.Info("execution dispatched", "executionId", executionID, "task", taskName)
	return nil
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
		if errors.Is(runErr, storage.ErrNotFound) {
			return &CloudError{Kind: CloudErrorKindConflict, Message: "execution not found"}
		}
		return &CloudError{Kind: CloudErrorKindTransient, Message: "failed to inspect execution for stop", Err: runErr}
	}

	if run.Status.IsTerminal() {
		return nil
	}

	return &CloudError{Kind: CloudErrorKindConflict, Message: "execution is not currently running"}
}

func (h *InboundHandler) HandleLogRequest(message protocol.LogRequestMessage) (protocol.LogResponseMessage, error) {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return NewLogResponseMessage(message.ID, message.ExecutionID, "", 0, true), &CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	run, err := h.runRepo.GetRunByExternalExecutionID(executionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return NewLogResponseMessage(message.ID, executionID, "", message.Offset, true), nil
		}
		return NewLogResponseMessage(message.ID, executionID, "", message.Offset, true), &CloudError{Kind: CloudErrorKindTransient, Message: "failed to query execution logs", Err: err}
	}

	result, readErr := readExecutionLogChunk(run, h.logDir, message.Offset, message.Limit)
	if readErr != nil {
		return NewLogResponseMessage(message.ID, executionID, "", message.Offset, true), &CloudError{Kind: CloudErrorKindTransient, Message: "failed to read execution logs", Err: readErr}
	}

	encoded := base64.StdEncoding.EncodeToString(result.Data)
	return NewLogResponseMessage(message.ID, executionID, encoded, result.Offset, result.Final), nil
}

func (h *InboundHandler) HandleLogListen(message protocol.LogListenMessage) error {
	executionID := strings.TrimSpace(message.ExecutionID)
	if executionID == "" {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "executionId is required"}
	}

	var offset int64
	run, err := h.runRepo.GetRunByExternalExecutionID(executionID)
	if err == nil {
		offset = getLogFileSize(run, h.logDir)
	}

	h.mu.Lock()
	h.logListeners[executionID] = &logListener{Offset: offset}
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

// ClaimLogChunkOffset atomically reads the current offset for a log listener and
// advances it by chunkLen bytes. It returns the offset at which the chunk should
// be emitted, so that concurrent goroutines always receive non-overlapping,
// ordered offset slots even when batches arrive simultaneously.
func (h *InboundHandler) ClaimLogChunkOffset(executionID string, chunkLen int64) (offset int64, exists bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.logListeners[executionID]
	if !ok {
		return 0, false
	}
	offset = l.Offset
	l.Offset += chunkLen
	return offset, true
}

// RemoveLogListener removes a log listener and returns its final offset.
func (h *InboundHandler) RemoveLogListener(executionID string) (int64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.logListeners[executionID]
	if !ok {
		return 0, false
	}
	offset := l.Offset
	delete(h.logListeners, executionID)
	return offset, true
}

// ClearLogListeners removes all active log listeners (e.g. on reconnect).
func (h *InboundHandler) ClearLogListeners() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logListeners = make(map[string]*logListener)
}

func runtimeTriggerOptions(executionID string) runtime.TriggerRunOptions {
	return runtime.TriggerRunOptions{
		TriggeredBy:         model.TriggeredByCloud,
		ExternalExecutionID: executionID,
	}
}
