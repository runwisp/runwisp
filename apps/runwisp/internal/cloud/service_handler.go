// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

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
//   - A cloud-declared service, addressed by a ULID: built from scratch and
//     brought up to its instance count when autostart is set (unchanged).
//
// Unlike an execution dispatch, a service is a desired-state declaration: the
// daemon's supervisor keeps `instances` copies alive, restarting them with the
// configured backoff. The task is registered with Restart=always + Kind=service
// so the supervisor owns its lifecycle (see retry.ShouldRestart).
func (h *InboundHandler) HandleServiceApply(message protocol.ServiceApplyMessage) error {
	svc := message.Service
	if svc == nil {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "service is required"}
	}

	name, existing, err := h.resolveServiceTarget(svc.TaskID, svc.TaskName)
	if err != nil {
		return err
	}
	if existing {
		base, _ := h.taskManager.GetTask(name)
		if err := h.mergeServiceApply(base, svc); err != nil {
			return err
		}
		h.taskManager.UpsertTask(base)
		slog.Info("service override applied", "task", name, "instances", base.Instances)
		return nil
	}

	task, err := h.buildServiceTask(svc)
	if err != nil {
		return err
	}

	h.taskManager.UpsertTask(task)

	if svc.Autostart {
		// Cloud-initiated: attribute the start to the control plane so the run
		// is auditable as cloud-originated (supervisor-driven restarts from here
		// on are TriggeredByService).
		if startErr := h.taskManager.StartServiceInstances(task.Name, model.TriggeredByCloud); startErr != nil {
			return &CloudError{Kind: CloudErrorKindConflict, Message: startErr.Error()}
		}
	}

	slog.Info("service applied", "task", task.Name, "instances", task.Instances, "autostart", svc.Autostart)
	return nil
}

// HandleServiceControl starts, stops, or restarts an already-applied service.
// The action enum is gated by the protocol; an unknown value is treated as a
// validation error rather than silently ignored.
func (h *InboundHandler) HandleServiceControl(message protocol.ServiceControlMessage) error {
	taskName, _, err := h.resolveServiceTarget(message.TaskID, "")
	if err != nil {
		return err
	}
	if taskName == "" {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "taskId is required"}
	}
	if message.Action == nil {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "action is required"}
	}

	action, _ := message.Action.Value().(string)
	switch action {
	case "start":
		err = h.taskManager.StartServiceInstances(taskName, model.TriggeredByCloud)
	case "stop":
		err = h.taskManager.StopService(taskName)
	case "restart":
		err = h.taskManager.RestartServiceInstances(taskName)
	default:
		return &CloudError{Kind: CloudErrorKindValidation, Message: fmt.Sprintf("unknown service action %q", action)}
	}
	if err != nil {
		return &CloudError{Kind: CloudErrorKindConflict, Message: err.Error()}
	}

	slog.Info("service control applied", "task", taskName, "action", action)
	return nil
}

// HandleServiceRemove tears down a previously declared service: it cancels the
// service's instances and drops its task from the runner. Only an existing
// service resolves here — resolveServiceTarget rejects a non-service name — so a
// remove can never delete an ordinary TOML task. A taskId that matches nothing
// is a no-op (the desired end state, service-gone, already holds).
func (h *InboundHandler) HandleServiceRemove(message protocol.ServiceRemoveMessage) error {
	name, existing, err := h.resolveServiceTarget(message.TaskID, "")
	if err != nil {
		return err
	}
	if !existing {
		return nil
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
		return nil, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: fmt.Sprintf("failed to parse execution def: %v", err),
		}
	}

	status := h.availability.ForType(execDef.ExecType())
	if !status.Available {
		return nil, &CloudError{
			Kind:    CloudErrorKindConflict,
			Message: fmt.Sprintf("execution type %q not available: %s", execDef.ExecType(), status.Reason),
		}
	}

	taskName := sanitizeCloudTaskName(svc.TaskID)
	if taskName == "" {
		taskName = sanitizeCloudTaskName(svc.TaskName)
	}
	if taskName == "" {
		taskName = "cloud-service"
	}

	if svc.Instances > config.MaxServiceInstances {
		return nil, &CloudError{
			Kind:    CloudErrorKindValidation,
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
	}

	if svc.RestartDelay > 0 {
		task.RestartDelay = time.Duration(svc.RestartDelay) * time.Millisecond
	}
	// Wire field is backoffResetAfter; the daemon models this uptime-resets-the-
	// restart-counter threshold as HealthyAfter (which also clears the
	// failed-start streak — a superset of the cloud-side semantics).
	if svc.BackoffResetAfter > 0 {
		task.HealthyAfter = time.Duration(svc.BackoffResetAfter) * time.Millisecond
	}
	if svc.RestartBackoff != nil {
		if s, ok := svc.RestartBackoff.Value().(string); ok && s != "" {
			task.RestartBackoff = s
		}
	}

	applyServiceTaskConfig(task, svc.TaskConfig)

	return task, nil
}

// resolveServiceTarget maps a cloud-supplied taskId/taskName onto a registered
// task name. A synced TOML service is addressed by its bare name, so a literal
// registry hit against a *service* means "merge onto this existing definition"
// (existing=true). A cloud-declared service carries a ULID with no literal
// match; it falls back to the sanitized cloud-<id> form (existing=false) used
// by buildServiceTask, so control/status/apply all resolve to the same name.
//
// A bare-name hit on a *non-service* task (a cron/one-shot TOML task) is
// rejected: cloud service handling must never merge onto — and so overwrite the
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
				return "", false, &CloudError{
					Kind:    CloudErrorKindValidation,
					Message: fmt.Sprintf("task %q is not a service; a cloud service message cannot target it", n),
				}
			}
			return n, true, nil
		}
	}
	if n := sanitizeCloudTaskName(taskID); n != "" {
		return n, false, nil
	}
	return sanitizeCloudTaskName(taskName), false, nil
}

// mergeServiceApply overlays the fields a service:apply payload carries onto an
// existing (synced TOML) service definition, leaving every field the payload
// does not address untouched. The cloud reasserts the full effective spec on
// each apply, so absent/zero fields mean "keep the current value". script is
// special: a synced service's cloud Script is a config-reference placeholder the
// cloud omits unless the operator overrode the command, so it is applied only
// when the payload actually carries one.
//
// "Omits" is logical, not literal: the wire field is non-omitempty, so a nil
// cloud Script marshals to the JSON literal `null` rather than being dropped.
// Treat that `null` exactly like an absent field — keep the live TOML command —
// instead of feeding it to ParseExecutionDef (which rejects it). Only a real def
// overrides the command.
func (h *InboundHandler) mergeServiceApply(task *model.Task, svc *protocol.Service) error {
	if len(svc.Script) > 0 && string(svc.Script) != "null" {
		execDef, err := model.ParseExecutionDef(svc.Script)
		if err != nil {
			return &CloudError{
				Kind:    CloudErrorKindValidation,
				Message: fmt.Sprintf("failed to parse execution def: %v", err),
			}
		}
		if status := h.availability.ForType(execDef.ExecType()); !status.Available {
			return &CloudError{
				Kind:    CloudErrorKindConflict,
				Message: fmt.Sprintf("execution type %q not available: %s", execDef.ExecType(), status.Reason),
			}
		}
		task.ExecutionDef = execDef
	}

	if svc.Instances > config.MaxServiceInstances {
		return &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: fmt.Sprintf("invalid instances for service %s: must be <= %d", task.Name, config.MaxServiceInstances),
		}
	}
	if svc.Instances >= 1 {
		task.Instances = svc.Instances
		task.MaxConcurrent = svc.Instances
	}
	if svc.RestartDelay > 0 {
		task.RestartDelay = time.Duration(svc.RestartDelay) * time.Millisecond
	}
	if svc.BackoffResetAfter > 0 {
		task.HealthyAfter = time.Duration(svc.BackoffResetAfter) * time.Millisecond
	}
	if svc.RestartBackoff != nil {
		if s, ok := svc.RestartBackoff.Value().(string); ok && s != "" {
			task.RestartBackoff = s
		}
	}

	applyServiceTaskConfig(task, svc.TaskConfig)
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
