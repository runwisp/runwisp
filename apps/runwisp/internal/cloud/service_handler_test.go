// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
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
