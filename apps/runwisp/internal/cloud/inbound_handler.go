// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
	"log/slog"
)

// LogChunkInterval is the minimum gap between consecutive `log:chunk`
// emissions to a single listener. Bytes that arrive within the window are
// coalesced and emitted at the next opportunity.
const LogChunkInterval = 2 * time.Second

type logListener struct {
	Offset    int64
	Buffer    []byte
	LastSent  time.Time
	FlushTime time.Time
	Timer     *time.Timer
}

// InboundHandler processes inbound WebSocket messages, encapsulating
// domain logic for execution dispatch, stop, and log operations.
type InboundHandler struct {
	taskManager     runtime.TaskRunner
	runRepo         storage.RunRepository
	logDir          string
	availability    executor.Availability
	queueExecUpdate func(protocol.ExecutionUpdateMessage)
	uploader        *LogUploader

	mu           sync.Mutex
	logListeners map[string]*logListener
}

func NewInboundHandler(
	taskManager runtime.TaskRunner,
	runRepo storage.RunRepository,
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
		logListeners:    make(map[string]*logListener),
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
		if h.uploader != nil {
			h.uploader.Forget(executionID)
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

// PendingChunk is the result of buffering a log line for a listener.
// When Ready is true the caller must emit a `log:chunk` message with the
// returned Offset and Data; when false the bytes are coalesced into the
// listener buffer and a deferred flush is scheduled.
type PendingChunk struct {
	Ready  bool
	Offset int64
	Data   []byte
}

// BufferLogChunk appends bytes for a listener and returns a chunk to emit
// immediately (when more than LogChunkInterval has passed since the last
// emission) or buffers them for later flush. Returns ok=false when no
// listener is registered for the execution. flush, when non-nil, is invoked
// from a timer goroutine for deferred emissions.
func (h *InboundHandler) BufferLogChunk(executionID string, line []byte, flush func(executionID string, offset int64, data []byte)) (PendingChunk, bool) {
	h.mu.Lock()
	l, ok := h.logListeners[executionID]
	if !ok {
		h.mu.Unlock()
		return PendingChunk{}, false
	}

	now := time.Now()
	l.Buffer = append(l.Buffer, line...)
	if l.LastSent.IsZero() || now.Sub(l.LastSent) >= LogChunkInterval {
		offset := l.Offset
		data := l.Buffer
		l.Offset += int64(len(data))
		l.Buffer = nil
		l.LastSent = now
		if l.Timer != nil {
			l.Timer.Stop()
			l.Timer = nil
			l.FlushTime = time.Time{}
		}
		h.mu.Unlock()
		return PendingChunk{Ready: true, Offset: offset, Data: data}, true
	}

	if l.Timer == nil && flush != nil {
		fire := l.LastSent.Add(LogChunkInterval)
		l.FlushTime = fire
		delay := time.Until(fire)
		if delay < 0 {
			delay = 0
		}
		execID := executionID
		l.Timer = time.AfterFunc(delay, func() {
			h.flushLogChunk(execID, flush)
		})
	}
	h.mu.Unlock()
	return PendingChunk{}, true
}

func (h *InboundHandler) flushLogChunk(executionID string, flush func(executionID string, offset int64, data []byte)) {
	h.mu.Lock()
	l, ok := h.logListeners[executionID]
	if !ok || len(l.Buffer) == 0 {
		if ok {
			l.Timer = nil
			l.FlushTime = time.Time{}
		}
		h.mu.Unlock()
		return
	}
	offset := l.Offset
	data := l.Buffer
	l.Offset += int64(len(data))
	l.Buffer = nil
	l.LastSent = time.Now()
	l.Timer = nil
	l.FlushTime = time.Time{}
	h.mu.Unlock()

	if flush != nil {
		flush(executionID, offset, data)
	}
}

// RemoveLogListener removes a log listener and returns its final offset
// plus any buffered bytes that were waiting on the rate-limit window.
func (h *InboundHandler) RemoveLogListener(executionID string) (offset int64, pending []byte, exists bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l, ok := h.logListeners[executionID]
	if !ok {
		return 0, nil, false
	}
	if l.Timer != nil {
		l.Timer.Stop()
	}
	offset = l.Offset
	pending = l.Buffer
	l.Offset += int64(len(l.Buffer))
	l.Buffer = nil
	delete(h.logListeners, executionID)
	return offset, pending, true
}

// ClearLogListeners removes all active log listeners (e.g. on reconnect).
func (h *InboundHandler) ClearLogListeners() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.logListeners {
		if l.Timer != nil {
			l.Timer.Stop()
		}
	}
	h.logListeners = make(map[string]*logListener)
}

func runtimeTriggerOptions(executionID string) runtime.TriggerRunOptions {
	return runtime.TriggerRunOptions{
		TriggeredBy:         model.TriggeredByCloud,
		ExternalExecutionID: executionID,
	}
}
