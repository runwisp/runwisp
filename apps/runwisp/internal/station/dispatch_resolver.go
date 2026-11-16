// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"cmp"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// resolveDispatchTask resolves a dispatch to a runnable task name: one of this
// daemon's TOML-defined tasks for a config execution, otherwise an ephemeral
// task synthesized from the inline definition. For a TOML task it also returns
// the run's overlay of the control plane's overrides (configOverlay).
func (h *InboundHandler) resolveDispatchTask(dispatch protocol.ExecutionDispatch) (string, func(*model.Task), error) {
	execDef, err := h.parseAvailableDef(dispatch.Spec)
	if err != nil {
		return "", nil, err
	}

	if cfg, ok := execDef.(*model.ConfigExecution); ok {
		// Config type means "resolve from this daemon's local tasks".
		task, exists := h.taskManager.GetTask(cfg.TaskName)
		if !exists {
			return "", nil, newError(protocol.ErrorCodeNotFound, "config task '%s' not found", cfg.TaskName)
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
			return "", nil, newError(protocol.ErrorCodeForbidden, "config task '%s' is a service and cannot be triggered by the station", cfg.TaskName)
		case model.TriggerBlockedManualDisabled:
			return "", nil, newError(protocol.ErrorCodeForbidden, "config task '%s' has manual_trigger disabled and cannot be triggered by the station", cfg.TaskName)
		}
		overlay, err := h.configOverlay(task, dispatch)
		if err != nil {
			return "", nil, err
		}
		return cfg.TaskName, overlay, nil
	}

	task, err := buildDynamicStationTask(dispatch, execDef)
	if err != nil {
		return "", nil, err
	}
	// sanitizeStationTaskName only prefixes "station-"; the rest of the name is
	// peer-supplied, so an inline dispatch can land on a locally-defined task
	// named station-*. Upserting over it would let the peer replace a disk-defined
	// task's execution (and have the ephemeral reaper delete it afterwards),
	// violating "run= comes from disk only". resolveServiceTarget guards the
	// service path the same way.
	if existing, ok := h.taskManager.GetTask(task.Name); ok && !existing.Ephemeral {
		return "", nil, newError(protocol.ErrorCodeConflict, "task %q is defined locally; an inline station execution cannot overwrite it", task.Name)
	}
	h.taskManager.UpsertTask(task)
	return task.Name, nil, nil
}

// configOverlay applies the station's overrides of a TOML task to one run: the
// run options (failures, env, graceful stop, log cap) and the timeout. The task's own definition is untouched, so the TOML's schedules and
// local triggers keep running it as written. A station that predates
// overrides echoes the TOML's own synced values back, which the overlay
// re-applies unchanged; env goes through the same allow_station_dispatch gate
// as a service's only when it differs from the TOML's, so that echo never
// trips it. An invalid list rejects the dispatch here, before a run exists.
func (h *InboundHandler) configOverlay(task *model.Task, dispatch protocol.ExecutionDispatch) (func(*model.Task), error) {
	cfg, timeoutMs := dispatch.Options, dispatch.TimeoutMs
	if cfg == nil && timeoutMs <= 0 {
		return nil, nil
	}
	if cfg != nil && len(cfg.Env) > 0 && !maps.Equal(cfg.Env, task.Env) {
		if err := h.checkEnvOverrideAllowed(task, cfg); err != nil {
			return nil, err
		}
	}
	probe := *task
	if err := applyRunOptions(&probe, cfg); err != nil {
		return nil, err
	}
	return func(t *model.Task) {
		_ = applyRunOptions(t, cfg) // validated on the probe above
		if timeoutMs > 0 {
			d := time.Duration(timeoutMs) * time.Millisecond
			t.Timeout = &d
		}
	}, nil
}

// parseAvailableDef parses a station-supplied execution spec and rejects
// types this daemon doesn't know (unsupported) or won't run for the station
// (forbidden by local policy, or unavailable).
func (h *InboundHandler) parseAvailableDef(spec json.RawMessage) (model.ExecutionDef, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(spec, &head); err != nil {
		return nil, newError(protocol.ErrorCodeInvalid, "spec must be an object with a type: %v", err)
	}
	if _, known := featureForExecType(head.Type); !known {
		return nil, newError(protocol.ErrorCodeUnsupported, "execution type %q is not supported by this daemon", head.Type)
	}
	execDef, err := model.ParseExecutionDef(spec)
	if err != nil {
		return nil, newError(protocol.ErrorCodeInvalid, "invalid spec: %v", err)
	}
	if status := h.liveAvailability().ForType(execDef.ExecType()); !status.Available {
		code := protocol.ErrorCodeUnavailable
		if status.Policy {
			code = protocol.ErrorCodeForbidden
		}
		return nil, newError(code, "execution type %q not available: %s", execDef.ExecType(), status.Reason)
	}
	return execDef, nil
}

func buildDynamicStationTask(dispatch protocol.ExecutionDispatch, execDef model.ExecutionDef) (*model.Task, error) {
	ref := cmp.Or(dispatch.Task, &protocol.TaskRef{})
	taskName := sanitizeStationTaskName(ref.ID)
	if taskName == "" {
		taskName = sanitizeStationTaskName(ref.Name)
	}
	if taskName == "" {
		taskName = "station-inline"
	}

	task := &model.Task{
		Name:         taskName,
		ExecutionDef: execDef,
		// The station already enforced the task's and the runner's concurrency
		// limits before it dispatched, so the daemon must not add a second
		// limit of its own: a cap of 1 here queued every overlapping dispatch
		// locally while the station's deadline for it kept counting.
		MaxConcurrent: config.MaxConcurrentCap,
		OnOverlap:     model.PolicyQueue,
		// MaxQueued is left unset: MaxQueuedValue bounds the queue at the same
		// default a TOML queue task gets.
		// One-shot station dispatch: the run manager reaps this task (and its
		// queue-drain goroutine) once the run retires, so a long-running daemon
		// doesn't leak state per distinct dispatched name.
		Ephemeral: true,
	}

	if dispatch.TimeoutMs > 0 {
		timeout := time.Duration(dispatch.TimeoutMs) * time.Millisecond
		task.Timeout = &timeout
	}

	if err := applyRunOptions(task, dispatch.Options); err != nil {
		return nil, err
	}

	// An inline dispatch declares no TOML params, so every inputs key
	// would otherwise fail ResolveParamValues as "unknown parameter". Declare
	// each as an env-kind param on the fly so the supplied values land in the
	// run's process environment under their own key.
	for key := range dispatch.Inputs {
		task.Parameters = append(task.Parameters, model.TaskParam{Kind: model.ParamEnv, Key: key})
	}

	return task, nil
}

// applyRunOptions overlays the optional per-run/per-process knobs the station
// carries in options (execution:dispatch and service:apply) onto task. Each
// field is honored only when set; an omitted/zero value leaves the daemon
// default in place. failures uses the TOML `failures` grammar resolved against
// the built-in default; an invalid list rejects the whole message.
func applyRunOptions(task *model.Task, cfg *protocol.RunOptions) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.Failures) > 0 {
		spec, err := model.ParseFailures(cfg.Failures)
		if err != nil {
			return newError(protocol.ErrorCodeInvalid, "invalid options.failures: %v", err)
		}
		task.Failures = spec.Resolve(model.DefaultFailures())
	}
	if len(cfg.Env) > 0 {
		task.Env = cfg.Env
	}
	if cfg.GracefulStopMs > 0 {
		g := time.Duration(cfg.GracefulStopMs) * time.Millisecond
		task.GracefulStop = &g
	}
	if cfg.LogMaxBytes > 0 {
		task.LogMaxSize = cfg.LogMaxBytes
	}
	if cfg.LogOnFull != "" {
		task.LogOnFull = cfg.LogOnFull
	}
	return nil
}

func sanitizeStationTaskName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.Trim("station-"+model.SanitizeTaskName(raw), "-_")
}
