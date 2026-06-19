// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"encoding/json"
	"errors"
	"testing"

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
	assert.Equal(t, "exponential", task.RestartBackoff)
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
	logOnFull := protocol.ServiceTaskConfigLogOnFullKillTask
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
	assert.Equal(t, "kill_task", task.LogOnFull)
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
