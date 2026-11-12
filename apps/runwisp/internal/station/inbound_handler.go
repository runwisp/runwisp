// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// InboundHandler carries out the station's execution and service commands
// against the task manager.
type InboundHandler struct {
	taskManager  TaskRunner
	runRepo      ExternalRunGetter
	logDir       string
	availability executor.Availability
	// localTasks, when set, makes Config availability live (see
	// liveAvailability); nil keeps the boot-time value.
	localTasks *runtime.TaskRegistry
	// appendUpdate stores an execution update for delivery (the outbox).
	appendUpdate func(*protocol.ExecutionUpdate)
	uploader     *LogUploader
	tracker      *ExecutionTracker
	now          func() time.Time
}

// InboundHandlerDeps bundles the collaborators an InboundHandler needs.
type InboundHandlerDeps struct {
	TaskManager  TaskRunner
	RunRepo      ExternalRunGetter
	LogDir       string
	Availability executor.Availability
	LocalTasks   *runtime.TaskRegistry
	AppendUpdate func(*protocol.ExecutionUpdate)
	Uploader     *LogUploader
	Tracker      *ExecutionTracker
	// Now defaults to time.Now; it decides whether a dispatch's notAfter passed.
	Now func() time.Time
}

func NewInboundHandler(deps InboundHandlerDeps) *InboundHandler {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &InboundHandler{
		taskManager:  deps.TaskManager,
		runRepo:      deps.RunRepo,
		logDir:       deps.LogDir,
		availability: deps.Availability,
		localTasks:   deps.LocalTasks,
		appendUpdate: deps.AppendUpdate,
		uploader:     deps.Uploader,
		tracker:      deps.Tracker,
		now:          now,
	}
}

// liveAvailability is the boot-time availability with Config recomputed from
// the current TOML task set, which a reload can grow from or shrink to empty.
// The other types stay fixed at boot by allow_station_dispatch.
func (h *InboundHandler) liveAvailability() executor.Availability {
	avail := h.availability
	if h.localTasks != nil {
		avail.Config = executor.ConfigStatus(h.localTasks.Len() > 0)
	}
	return avail
}

// HandleExecutionDispatch accepts and triggers a dispatched execution. A nil
// error is the acceptance; the run's progress follows as execution updates.
func (h *InboundHandler) HandleExecutionDispatch(ctx context.Context, d protocol.ExecutionDispatch) error {
	executionID, err := requireExecutionID(d.ExecutionID)
	if err != nil {
		return err
	}

	// Idempotent re-dispatch: the station re-sends a dispatch whose result it
	// never got. A known execution is accepted again without a second run.
	if h.isDuplicateDispatch(ctx, executionID) {
		slog.Info("duplicate execution dispatch accepted again", "executionId", executionID)
		return nil
	}
	if d.NotAfter != nil && h.now().After(*d.NotAfter) {
		h.tracker.Release(executionID)
		return newError(protocol.ErrorCodeExpired, "dispatch arrived after its notAfter (%s)", d.NotAfter.UTC().Format(time.RFC3339))
	}

	// Record the pending archive BEFORE the run starts: a crash after the
	// trigger would otherwise leave a log nobody uploads.
	if err := h.uploader.RegisterDispatch(ctx, executionID, d.LogStream); err != nil {
		slog.Warn("failed to record the execution's log for archival", "executionId", executionID, "err", err)
	}

	taskName, overlay, err := h.resolveDispatchTask(d)
	if err != nil {
		h.tracker.Release(executionID)
		h.uploader.forget(ctx, executionID)
		return err
	}

	// TOML-defined tasks resolve inputs against their declared params;
	// buildDynamicStationTask declares an env-kind param per key for inline
	// executions, so both paths resolve the same way here.
	run, err := h.taskManager.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy: model.TriggeredByStation,
		ExecutionID: executionID,
		// The protocol carries plain string values (no explicit-omit state), so
		// every supplied key is a present value; absent keys use the default.
		Params:  model.PointerValues(d.Inputs),
		Overlay: overlay,
	})
	if err != nil {
		if run != nil {
			// The run exists and already ended (skipped, queue full); its
			// finished update tells the station why.
			slog.Info("dispatched execution ended before it started", "executionId", executionID, "reason", err)
			return nil
		}
		h.tracker.Release(executionID)
		h.uploader.forget(ctx, executionID)
		return newError(protocol.ErrorCodeConflict, "%s", err.Error())
	}

	slog.Info("execution dispatched", "executionId", executionID, "task", taskName)
	return nil
}

// isDuplicateDispatch reports whether the daemon already knows the execution.
// It atomically reserves the executionId: a failed reservation means another
// dispatch for the same id is accepted or running. A fresh reservation is held
// until the run finishes (tracker.Remove) or the dispatch is rejected
// (tracker.Release). The persisted-run lookup covers a daemon restart that lost
// the in-memory reservation; a finished run's update is stored again so a
// station that lost track of it converges instead of re-running the task.
func (h *InboundHandler) isDuplicateDispatch(ctx context.Context, executionID string) bool {
	if !h.tracker.Reserve(executionID) {
		return true
	}
	run, err := h.runRepo.GetRunByExecutionID(ctx, executionID)
	if err != nil || run == nil {
		return false
	}
	if run.Status.IsTerminal() {
		h.appendUpdate(executionUpdate(run))
	}
	// A run already exists, so this dispatch won't create one.
	h.tracker.Release(executionID)
	return true
}

// HandleExecutionStop stops a running execution. Stopping one that already
// finished succeeds: the station's wish already holds.
func (h *InboundHandler) HandleExecutionStop(ctx context.Context, stop protocol.ExecutionStop) error {
	executionID, err := requireExecutionID(stop.ExecutionID)
	if err != nil {
		return err
	}
	grace := time.Duration(stop.GracefulStopMs) * time.Millisecond
	if err := h.taskManager.StopRunByExecutionID(executionID, grace, stop.Force); err == nil {
		return nil
	}

	run, runErr := h.runRepo.GetRunByExecutionID(ctx, executionID)
	if runErr != nil {
		if errors.Is(runErr, storage.ErrNotFound) {
			return newError(protocol.ErrorCodeNotFound, "execution not found")
		}
		return &StationError{Code: protocol.ErrorCodeInternal, Message: "failed to inspect execution for stop", Retryable: true, Err: runErr}
	}
	if run.Status.IsTerminal() {
		return nil
	}
	return newError(protocol.ErrorCodeConflict, "execution is not running yet")
}

// requireExecutionID trims raw and rejects an empty id.
func requireExecutionID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", newError(protocol.ErrorCodeInvalid, "executionId is required")
	}
	return id, nil
}
