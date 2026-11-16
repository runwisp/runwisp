// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"encoding/json"
	"strings"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
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
func (h *InboundHandler) HandleServiceApply(svc protocol.ServiceApply) error {
	if svc.Settings == nil {
		svc.Settings = &protocol.ServiceSettings{}
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
			return newError(protocol.ErrorCodeNotFound, "service %q no longer exists", name)
		}
		slog.Info("service override applied", "task", name, "instances", applied.Instances)
		return nil
	}

	task, err := h.buildServiceTask(svc)
	if err != nil {
		return err
	}

	h.taskManager.UpsertTask(task)

	if svc.Settings.Autostart {
		// Station-initiated: attribute the start to the station so the run is
		// auditable as station-originated (supervisor-driven restarts from here
		// on are TriggeredByService).
		if startErr := h.taskManager.StartServiceInstances(task.Name, model.TriggeredByStation); startErr != nil {
			return newError(protocol.ErrorCodeConflict, "%s", startErr.Error())
		}
	}

	slog.Info("service applied", "task", task.Name, "instances", task.Instances, "autostart", svc.Settings.Autostart)
	return nil
}

// HandleServiceControl starts, stops, or restarts an already-applied service.
// The action enum is gated by the protocol; an unknown value is treated as a
// validation error rather than silently ignored. A service with
// manual_trigger = false refuses every action here: it's locked to its
// restart policy, changeable only by editing runwisp.toml and reloading, the
// same gate resolveDispatchTask enforces for a manual_trigger = false task.
func (h *InboundHandler) HandleServiceControl(message protocol.ServiceControl) error {
	taskName, _, err := h.resolveServiceTarget(message.TaskID, "")
	if err != nil {
		return err
	}
	if taskName == "" {
		return newError(protocol.ErrorCodeInvalid, "taskId is required")
	}
	if message.Action == "" {
		return newError(protocol.ErrorCodeInvalid, "action is required")
	}
	if task, ok := h.taskManager.GetTask(taskName); ok && !task.ManuallyControllable() {
		return newError(protocol.ErrorCodeForbidden, "service %q has manual_trigger disabled and cannot be controlled by the station", taskName)
	}

	switch message.Action {
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
		return newError(protocol.ErrorCodeInvalid, "unknown service action %q", message.Action)
	}
	if err != nil {
		return newError(protocol.ErrorCodeConflict, "%s", err.Error())
	}

	slog.Info("service control applied", "task", taskName, "action", message.Action)
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
func (h *InboundHandler) HandleServiceRemove(message protocol.ServiceRemove) error {
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
		return newError(protocol.ErrorCodeForbidden, "service %q is defined in runwisp.toml; the station cannot remove it", name)
	}
	h.taskManager.RemoveTask(name)
	slog.Info("service removed", "task", name)
	return nil
}

// buildServiceTask maps a service:apply payload onto a supervisable model.Task.
// The task name is derived the same way as an inline execution (sanitized
// taskId, falling back to taskName) so control messages and status reports
// resolve to the same registered task.
func (h *InboundHandler) buildServiceTask(svc protocol.ServiceApply) (*model.Task, error) {
	if specAbsent(svc.Spec) {
		return nil, newError(protocol.ErrorCodeInvalid, "spec is required for a service this daemon doesn't define")
	}
	execDef, err := h.parseAvailableDef(svc.Spec)
	if err != nil {
		return nil, err
	}
	settings := svc.Settings

	taskName := sanitizeStationTaskName(svc.TaskID)
	if taskName == "" {
		taskName = sanitizeStationTaskName(svc.TaskName)
	}
	if taskName == "" {
		taskName = "station-service"
	}

	if err := checkServiceInstances(taskName, settings.Instances); err != nil {
		return nil, err
	}
	instances := max(settings.Instances, 1)

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
		// The runtime creates the supervisor parked unless Autostart is set,
		// and StartServiceInstances is a no-op on a parked service.
		Autostart: settings.Autostart,
	}
	applyServiceSettings(task, settings)
	if err := applyRunOptions(task, svc.Options); err != nil {
		return nil, err
	}
	return task, nil
}

// applyServiceSettings overlays the restart knobs a service:apply carries;
// zero means "keep the current value". The wire's backoffResetAfterMs is the
// daemon's HealthyAfter (which also clears the failed-start streak, a superset
// of the station-side meaning).
func applyServiceSettings(task *model.Task, settings *protocol.ServiceSettings) {
	if settings.RestartDelayMs > 0 {
		d := time.Duration(settings.RestartDelayMs) * time.Millisecond
		task.RestartDelay = &d
	}
	if settings.BackoffResetAfterMs > 0 {
		d := time.Duration(settings.BackoffResetAfterMs) * time.Millisecond
		task.HealthyAfter = &d
	}
	if settings.RestartBackoff != "" {
		task.RestartBackoff = model.BackoffCurve(settings.RestartBackoff)
	}
}

// specAbsent reports a spec the station left out or sent as null, which on
// service:apply means "keep the daemon's own command".
func specAbsent(spec json.RawMessage) bool {
	return len(spec) == 0 || string(spec) == "null"
}

// checkServiceInstances rejects a service instance count above the cap TOML
// services are held to.
func checkServiceInstances(name string, instances int) error {
	if instances > config.MaxServiceInstances {
		return newError(protocol.ErrorCodeInvalid, "invalid instances for service %s: must be <= %d", name, config.MaxServiceInstances)
	}
	return nil
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
				return "", false, newError(protocol.ErrorCodeConflict, "task %q is not a service; a station service message cannot target it", n)
			}
			return n, true, nil
		}
	}
	if n := sanitizeStationTaskName(taskID); n != "" {
		return n, false, nil
	}
	return sanitizeStationTaskName(taskName), false, nil
}

// mergeServiceApply overlays the fields a service:apply carries onto an
// existing (synced TOML) service definition, leaving every field it does not
// address untouched. The station reasserts the full effective state on each
// apply, so absent/zero fields mean "keep the current value". spec is applied
// only when present: null or absent keeps the TOML command.
func (h *InboundHandler) mergeServiceApply(task *model.Task, svc protocol.ServiceApply) error {
	// manual_trigger = false locks a service to its TOML definition, the same
	// gate HandleServiceControl applies; an apply would otherwise rescale or
	// retune it.
	if !task.ManuallyControllable() {
		return newError(protocol.ErrorCodeForbidden, "service %q has manual_trigger disabled and cannot be changed by the station", task.Name)
	}
	if !specAbsent(svc.Spec) {
		execDef, err := h.parseAvailableDef(svc.Spec)
		if err != nil {
			return err
		}
		task.ExecutionDef = execDef
	}

	if err := checkServiceInstances(task.Name, svc.Settings.Instances); err != nil {
		return err
	}
	if svc.Settings.Instances >= 1 {
		task.Instances = svc.Settings.Instances
		task.MaxConcurrent = svc.Settings.Instances
	}
	applyServiceSettings(task, svc.Settings)

	if err := h.checkEnvOverrideAllowed(task, svc.Options); err != nil {
		return err
	}
	return applyRunOptions(task, svc.Options)
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
func (h *InboundHandler) checkEnvOverrideAllowed(task *model.Task, cfg *protocol.RunOptions) error {
	if cfg == nil || len(cfg.Env) == 0 {
		return nil
	}
	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return newError(protocol.ErrorCodeConflict, "env override for %q not available: it has no resolvable execution definition", task.Name)
	}
	if status := h.liveAvailability().ForType(execDef.ExecType()); !status.Available {
		code := protocol.ErrorCodeUnavailable
		if status.Policy {
			code = protocol.ErrorCodeForbidden
		}
		return newError(code, "env override for %q not available: %s", task.Name, status.Reason)
	}
	return nil
}
