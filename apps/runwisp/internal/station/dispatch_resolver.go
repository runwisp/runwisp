// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"fmt"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
)

// resolveDispatchTask resolves a dispatch to a runnable task name. configBacked
// reports whether the task is one of this daemon's TOML-defined tasks (whose
// params come from runwisp.toml) as opposed to an inline ad-hoc execution
// (whose params buildDynamicStationTask synthesizes from inputValues' keys).
func (h *InboundHandler) resolveDispatchTask(dispatch *protocol.Execution) (taskName string, configBacked bool, err error) {
	execDef, err := model.ParseExecutionDef(dispatch.Script)
	if err != nil {
		return "", false, &StationError{
			Kind:    StationErrorKindValidation,
			Message: fmt.Sprintf("failed to parse execution def: %v", err),
		}
	}

	// Reject execution types the daemon doesn't allow for station dispatch.
	status := h.availability.ForType(execDef.ExecType())
	if !status.Available {
		return "", false, &StationError{
			Kind:    StationErrorKindConflict,
			Message: fmt.Sprintf("execution type %q not available: %s", execDef.ExecType(), status.Reason),
		}
	}

	if cfg, ok := execDef.(*model.ConfigExecution); ok {
		// Config type means "resolve from this daemon's local tasks".
		task, exists := h.taskManager.GetTask(cfg.TaskName)
		if !exists {
			return "", false, &StationError{Kind: StationErrorKindConflict, Message: fmt.Sprintf("config task '%s' not found", cfg.TaskName)}
		}
		// The control plane is an out-of-scheduler trigger like the REST surface,
		// so it honors the same gates via the same rule (model.Task.CheckTrigger).
		// [services.*] is never manually triggered (checked first — without it,
		// naming a service here would reserve it an extra instance outside its
		// restart policy, bypassing the allow_station_dispatch gate meant for that).
		// manual_trigger=false makes a task cron/schedule-only everywhere, not
		// just over HTTP.
		switch task.CheckTrigger() {
		case model.TriggerBlockedService:
			return "", false, &StationError{
				Kind:    StationErrorKindConflict,
				Message: fmt.Sprintf("config task '%s' is a service and cannot be triggered by the control plane", cfg.TaskName),
			}
		case model.TriggerBlockedManualDisabled:
			return "", false, &StationError{
				Kind:    StationErrorKindConflict,
				Message: fmt.Sprintf("config task '%s' has manual_trigger disabled and cannot be triggered by the control plane", cfg.TaskName),
			}
		}
		return cfg.TaskName, true, nil
	}

	task := buildDynamicStationTask(dispatch, execDef)
	// sanitizeStationTaskName only prefixes "station-"; the rest of the name is
	// peer-supplied, so an inline dispatch can land on a locally-defined task
	// named station-*. Upserting over it would let the peer replace a disk-defined
	// task's execution (and have the ephemeral reaper delete it afterwards),
	// violating "run= comes from disk only". resolveServiceTarget guards the
	// service path the same way.
	if existing, ok := h.taskManager.GetTask(task.Name); ok && !existing.Ephemeral {
		return "", false, &StationError{
			Kind:    StationErrorKindConflict,
			Message: fmt.Sprintf("task %q is defined locally; an inline station execution cannot overwrite it", task.Name),
		}
	}
	h.taskManager.UpsertTask(task)
	return task.Name, false, nil
}

func buildDynamicStationTask(dispatch *protocol.Execution, execDef model.ExecutionDef) *model.Task {
	taskName := sanitizeStationTaskName(dispatch.TaskID)
	if taskName == "" {
		taskName = sanitizeStationTaskName(dispatch.TaskName)
	}
	if taskName == "" {
		taskName = "station-inline"
	}

	task := &model.Task{
		Name:          taskName,
		ExecutionDef:  execDef,
		MaxConcurrent: 1,
		OnOverlap:     model.PolicyQueue,
		// Station dynamic tasks bypass config defaulting, so set the same bound a
		// TOML queue task gets — otherwise MaxQueued stays 0 (unbounded) and a
		// stream of dispatches to one slow name grows the queue without limit.
		MaxQueued: config.DefaultMaxQueued,
		// One-shot station dispatch: the run manager reaps this task (and its
		// queue-drain goroutine) once the run retires, so a long-running daemon
		// doesn't leak state per distinct dispatched name.
		Ephemeral: true,
	}

	if dispatch.Timeout > 0 {
		timeout := time.Duration(dispatch.Timeout) * time.Millisecond
		task.Timeout = &timeout
	}

	applyStationTaskConfig(task, dispatch.TaskConfig)

	// An inline dispatch declares no TOML params, so every inputValues key
	// would otherwise fail ResolveParamValues as "unknown parameter". Declare
	// each as an env-kind param on the fly so the supplied values land in the
	// run's process environment under their own key.
	for key := range dispatch.InputValues {
		task.Parameters = append(task.Parameters, model.TaskParam{Kind: model.ParamEnv, Key: key})
	}

	return task
}

// applyStationTaskConfig overlays the optional per-run execution knobs the station
// control plane carries in ExecutionPayload.taskConfig onto the dynamically
// built task.
func applyStationTaskConfig(task *model.Task, cfg *protocol.ExecutionTaskConfig) {
	if cfg == nil {
		return
	}
	logOnFull := ""
	if cfg.LogOnFull != nil {
		logOnFull, _ = cfg.LogOnFull.Value().(string)
	}
	applyTaskConfigKnobs(task, cfg.Env, cfg.GracefulStop, cfg.LogMaxSize, logOnFull)
}

// applyTaskConfigKnobs overlays the optional per-run/per-process execution knobs
// (env, graceful-stop, log limits) onto task. Shared by the execution- and
// service-dispatch paths, whose generated taskConfig messages are structurally
// identical but distinctly typed (ExecutionTaskConfig / ServiceTaskConfig). Each
// field is honored only when set; an omitted/zero value leaves the daemon
// default in place. gracefulStop arrives in milliseconds and is stored as a
// Duration.
func applyTaskConfigKnobs(task *model.Task, env map[string]string, gracefulStop, logMaxSize int, logOnFull string) {
	if len(env) > 0 {
		task.Env = env
	}
	if gracefulStop > 0 {
		g := time.Duration(gracefulStop) * time.Millisecond
		task.GracefulStop = &g
	}
	if logMaxSize > 0 {
		task.LogMaxSize = int64(logMaxSize)
	}
	if logOnFull != "" {
		task.LogOnFull = logOnFull
	}
}

func sanitizeStationTaskName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.Trim("station-"+model.SanitizeTaskName(raw), "-_")
}
