// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
)

// HandleServiceApply installs or updates a service. Two shapes resolve here:
//
//   - A synced TOML service, addressed by its bare name: the present payload
//     fields are merged onto the live definition (instances/restart/env/script)
//     while every TOML-only field (working_dir, shell, umask, run_user,
//     depends_on, secrets) is preserved, then re-upserted so the supervisor
//     rescales in place. Its running/stopped state is owned by service:control,
//     so an apply never starts it.
//   - A station-declared service, addressed by a ULID: built from scratch and
//     brought up to its instance count when autostart is set (unchanged).
//
// Unlike an execution dispatch, a service is a desired-state declaration: the
// daemon's supervisor keeps `instances` copies alive, restarting them with the
// configured backoff. The task is registered with Restart=always + Kind=service
// so the supervisor owns its lifecycle (see retry.ShouldRestart).
func (h *InboundHandler) HandleServiceApply(message protocol.ServiceApplyMessage) error {
	svc := message.Service
	if svc == nil {
		return &StationError{Kind: StationErrorKindValidation, Message: "service is required"}
	}

	name, existing, err := h.resolveServiceTarget(svc.TaskID, svc.TaskName)
	if err != nil {
		return err
	}
	if existing {
		var applied *model.Task
		found, mutateErr := h.taskManager.MutateTask(name, func(task *model.Task) error {
			if err := h.mergeServiceApply(task, svc); err != nil {
				return err
			}
			applied = task
			return nil
		})
		if mutateErr != nil {
			return mutateErr
		}
		if !found {
			// resolveServiceTarget confirmed the task existed moments ago; a
			// concurrent reload or service:remove can still have dropped it
			// in between. Fail closed rather than silently no-op.
			return &StationError{Kind: StationErrorKindConflict, Message: fmt.Sprintf("service %q no longer exists", name)}
		}
		slog.Info("service override applied", "task", name, "instances", applied.Instances)
		return nil
	}

	task, err := h.buildServiceTask(svc)
	if err != nil {
		return err
	}

	h.taskManager.UpsertTask(task)

	if svc.Autostart {
		// Station-initiated: attribute the start to the control plane so the run
		// is auditable as station-originated (supervisor-driven restarts from here
		// on are TriggeredByService).
		if startErr := h.taskManager.StartServiceInstances(task.Name, model.TriggeredByStation); startErr != nil {
			return &StationError{Kind: StationErrorKindConflict, Message: startErr.Error()}
		}
	}

	slog.Info("service applied", "task", task.Name, "instances", task.Instances, "autostart", svc.Autostart)
	return nil
}

// HandleServiceControl starts, stops, or restarts an already-applied service.
// The action enum is gated by the protocol; an unknown value is treated as a
// validation error rather than silently ignored. A service with
// manual_trigger = false refuses every action here: it's locked to its
// restart policy, changeable only by editing runwisp.toml and reloading, the
// same gate resolveDispatchTask enforces for a manual_trigger = false task.
func (h *InboundHandler) HandleServiceControl(message protocol.ServiceControlMessage) error {
	taskName, _, err := h.resolveServiceTarget(message.TaskID, "")
	if err != nil {
		return err
	}
	if taskName == "" {
		return &StationError{Kind: StationErrorKindValidation, Message: "taskId is required"}
	}
	if message.Action == nil {
		return &StationError{Kind: StationErrorKindValidation, Message: "action is required"}
	}
	if task, ok := h.taskManager.GetTask(taskName); ok && !task.ManuallyControllable() {
		return &StationError{
			Kind:    StationErrorKindConflict,
			Message: fmt.Sprintf("service %q has manual_trigger disabled and cannot be controlled by the control plane", taskName),
		}
	}

	action, _ := message.Action.Value().(string)
	switch action {
	case "start":
		// StartService (not StartServiceInstances): the control plane's "start"
		// means un-park it if the operator (or an earlier control message)
		// stopped it — StartServiceInstances alone is a no-op on a stopped
		// service by design, which would silently swallow this request.
		err = h.taskManager.StartService(taskName)
	case "stop":
		err = h.taskManager.StopService(taskName)
	case "restart":
		err = h.taskManager.RestartServiceInstances(taskName)
	default:
		return &StationError{Kind: StationErrorKindValidation, Message: fmt.Sprintf("unknown service action %q", action)}
	}
	if err != nil {
		return &StationError{Kind: StationErrorKindConflict, Message: err.Error()}
	}

	slog.Info("service control applied", "task", taskName, "action", action)
	return nil
}

// HandleServiceRemove tears down a previously declared service: it cancels the
// service's instances and drops its task from the runner. resolveServiceTarget
// rejects a non-service name, so a remove can never delete an ordinary TOML
// task, but a bare-name hit there matches ANY service, TOML-defined or
// station-declared, since both live in the same registry under the same name.
// Removal itself is gated separately below on StationDeclared: a TOML-defined
// [services.*] entry is owned by disk (removing it here would desync the
// running task set from runwisp.toml with no reload path back, since the
// reconciler's diff baseline still has it). A taskId that matches nothing is a
// no-op (the desired end state, service-gone, already holds).
func (h *InboundHandler) HandleServiceRemove(message protocol.ServiceRemoveMessage) error {
	name, existing, err := h.resolveServiceTarget(message.TaskID, "")
	if err != nil {
		return err
	}
	if !existing {
		return nil
	}
	task, ok := h.taskManager.GetTask(name)
	if !ok {
		// Removed concurrently between resolve and here; desired end state
		// (service-gone) already holds.
		return nil
	}
	if !task.StationDeclared {
		return &StationError{
			Kind:    StationErrorKindConflict,
			Message: fmt.Sprintf("service %q is defined in runwisp.toml; the control plane cannot remove it", name),
		}
	}
	h.taskManager.RemoveTask(name)
	slog.Info("service removed", "task", name)
	return nil
}

// buildServiceTask maps a service:apply payload onto a supervisable model.Task.
// The task name is derived the same way as an inline execution (sanitized
// taskId, falling back to taskName) so control messages and status reports
// resolve to the same registered task.
func (h *InboundHandler) buildServiceTask(svc *protocol.Service) (*model.Task, error) {
	execDef, err := model.ParseExecutionDef(svc.Script)
	if err != nil {
		return nil, &StationError{
			Kind:    StationErrorKindValidation,
			Message: fmt.Sprintf("failed to parse execution def: %v", err),
		}
	}

	status := h.availability.ForType(execDef.ExecType())
	if !status.Available {
		return nil, &StationError{
			Kind:    StationErrorKindConflict,
			Message: fmt.Sprintf("execution type %q not available: %s", execDef.ExecType(), status.Reason),
		}
	}

	taskName := sanitizeStationTaskName(svc.TaskID)
	if taskName == "" {
		taskName = sanitizeStationTaskName(svc.TaskName)
	}
	if taskName == "" {
		taskName = "station-service"
	}

	if svc.Instances > config.MaxServiceInstances {
		return nil, &StationError{
			Kind:    StationErrorKindValidation,
			Message: fmt.Sprintf("invalid instances for service %s: must be <= %d", taskName, config.MaxServiceInstances),
		}
	}
	instances := svc.Instances
	if instances < 1 {
		instances = 1
	}

	task := &model.Task{
		Name:         taskName,
		Kind:         model.KindService,
		ExecutionDef: execDef,
		// Services are supervisor-managed: every instance exit refills the slot.
		Restart:       model.RestartAlways,
		Instances:     instances,
		MaxConcurrent: instances,
		// Declared by the control plane, not TOML: only such a service may
		// later be torn down by service:remove (see resolveServiceTarget /
		// HandleServiceRemove).
		StationDeclared: true,
		// The protocol has no manual_trigger-equivalent field, so this must be
		// explicit: the zero value is false, which would lock the station out of
		// controlling the very service it just declared.
		ManualTrigger: true,
	}

	if svc.RestartDelay > 0 {
		d := time.Duration(svc.RestartDelay) * time.Millisecond
		task.RestartDelay = &d
	}
	// Wire field is backoffResetAfter; the daemon models this uptime-resets-the-
	// restart-counter threshold as HealthyAfter (which also clears the
	// failed-start streak — a superset of the station-side semantics).
	if svc.BackoffResetAfter > 0 {
		d := time.Duration(svc.BackoffResetAfter) * time.Millisecond
		task.HealthyAfter = &d
	}
	if svc.RestartBackoff != nil {
		if s, ok := svc.RestartBackoff.Value().(string); ok && s != "" {
			task.RestartBackoff = model.BackoffCurve(s)
		}
	}

	applyServiceTaskConfig(task, svc.TaskConfig)

	return task, nil
}

// resolveServiceTarget maps a station-supplied taskId/taskName onto a registered
// task name. A synced TOML service is addressed by its bare name, so a literal
// registry hit against a *service* means "merge onto this existing definition"
// (existing=true). A station-declared service carries a ULID with no literal
// match; it falls back to the sanitized station-<id> form (existing=false) used
// by buildServiceTask, so control/status/apply all resolve to the same name.
//
// A bare-name hit on a *non-service* task (a cron/one-shot TOML task) is
// rejected: station service handling must never merge onto — and so overwrite the
// disk-defined run= of — a task that isn't a service. This upholds the "run=
// comes from disk only" invariant against a control-plane peer aiming a
// service:apply at an ordinary task's name.
func (h *InboundHandler) resolveServiceTarget(taskID, taskName string) (name string, existing bool, err error) {
	for _, raw := range []string{taskID, taskName} {
		n := strings.TrimSpace(raw)
		if n == "" {
			continue
		}
		if t, ok := h.taskManager.GetTask(n); ok {
			if !t.Kind.IsService() {
				return "", false, &StationError{
					Kind:    StationErrorKindValidation,
					Message: fmt.Sprintf("task %q is not a service; a station service message cannot target it", n),
				}
			}
			return n, true, nil
		}
	}
	if n := sanitizeStationTaskName(taskID); n != "" {
		return n, false, nil
	}
	return sanitizeStationTaskName(taskName), false, nil
}

// mergeServiceApply overlays the fields a service:apply payload carries onto an
// existing (synced TOML) service definition, leaving every field the payload
// does not address untouched. The station reasserts the full effective spec on
// each apply, so absent/zero fields mean "keep the current value". script is
// special: a synced service's station Script is a config-reference placeholder the
// station omits unless the operator overrode the command, so it is applied only
// when the payload actually carries one.
//
// "Omits" is logical, not literal: the wire field is non-omitempty, so a nil
// station Script marshals to the JSON literal `null` rather than being dropped.
// Treat that `null` exactly like an absent field — keep the live TOML command —
// instead of feeding it to ParseExecutionDef (which rejects it). Only a real def
// overrides the command.
func (h *InboundHandler) mergeServiceApply(task *model.Task, svc *protocol.Service) error {
	if len(svc.Script) > 0 && string(svc.Script) != "null" {
		execDef, err := model.ParseExecutionDef(svc.Script)
		if err != nil {
			return &StationError{
				Kind:    StationErrorKindValidation,
				Message: fmt.Sprintf("failed to parse execution def: %v", err),
			}
		}
		if status := h.availability.ForType(execDef.ExecType()); !status.Available {
			return &StationError{
				Kind:    StationErrorKindConflict,
				Message: fmt.Sprintf("execution type %q not available: %s", execDef.ExecType(), status.Reason),
			}
		}
		task.ExecutionDef = execDef
	}

	if svc.Instances > config.MaxServiceInstances {
		return &StationError{
			Kind:    StationErrorKindValidation,
			Message: fmt.Sprintf("invalid instances for service %s: must be <= %d", task.Name, config.MaxServiceInstances),
		}
	}
	if svc.Instances >= 1 {
		task.Instances = svc.Instances
		task.MaxConcurrent = svc.Instances
	}
	if svc.RestartDelay > 0 {
		d := time.Duration(svc.RestartDelay) * time.Millisecond
		task.RestartDelay = &d
	}
	if svc.BackoffResetAfter > 0 {
		d := time.Duration(svc.BackoffResetAfter) * time.Millisecond
		task.HealthyAfter = &d
	}
	if svc.RestartBackoff != nil {
		if s, ok := svc.RestartBackoff.Value().(string); ok && s != "" {
			task.RestartBackoff = model.BackoffCurve(s)
		}
	}

	if err := h.checkEnvOverrideAllowed(task, svc.TaskConfig); err != nil {
		return err
	}

	applyServiceTaskConfig(task, svc.TaskConfig)
	return nil
}

// checkEnvOverrideAllowed puts a peer-supplied env override on the same
// allow_station_dispatch surface as a script override. env can change what a
// disk-defined command actually executes (NODE_OPTIONS, GIT_SSH_COMMAND,
// *_proxy) and it overlays on top of the inherited environment, so a peer must
// not reshape a TOML service's spawn env without the opt-in.
//
// Only env is gated. The instances/restart/log knobs are what the station
// legitimately re-asserts on every reconnect, so gating those would break sync
// on a dispatch-off daemon.
//
// The type comes from ResolvedExecutionDef, not the ExecutionDef field: the
// config loader only populates that field for compose-backed units, so an
// ordinary `[services.web] run = "..."` carries its command in Run with a nil
// ExecutionDef. Reading the field directly meant the one shape this gate exists
// to protect was also the one shape that dereferenced nil — a peer-triggerable
// crash instead of a verdict. A service with no resolvable definition at all
// can't be checked, so it is refused rather than waved through.
func (h *InboundHandler) checkEnvOverrideAllowed(task *model.Task, cfg *protocol.ServiceTaskConfig) error {
	if cfg == nil || len(cfg.Env) == 0 {
		return nil
	}
	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return &StationError{
			Kind:    StationErrorKindConflict,
			Message: fmt.Sprintf("env override for service %q not available: it has no resolvable execution definition", task.Name),
		}
	}
	if status := h.availability.ForType(execDef.ExecType()); !status.Available {
		return &StationError{
			Kind:    StationErrorKindConflict,
			Message: fmt.Sprintf("env override for service %q not available: %s", task.Name, status.Reason),
		}
	}
	return nil
}

// applyServiceTaskConfig overlays the optional per-process knobs a service
// carries onto the built task. The service-scoped ServiceTaskConfig is a
// distinct generated type from the execution-side ExecutionTaskConfig but has
// the same shape, so both funnel into applyTaskConfigKnobs.
func applyServiceTaskConfig(task *model.Task, cfg *protocol.ServiceTaskConfig) {
	if cfg == nil {
		return
	}
	logOnFull := ""
	if cfg.LogOnFull != nil {
		logOnFull, _ = cfg.LogOnFull.Value().(string)
	}
	applyTaskConfigKnobs(task, cfg.Env, cfg.GracefulStop, cfg.LogMaxSize, logOnFull)
}
