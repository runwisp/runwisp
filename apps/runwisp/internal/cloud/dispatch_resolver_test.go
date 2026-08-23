// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakeTaskRunner for dispatch resolver tests ---

type fakeTaskRunner struct {
	tasks      map[string]*model.Task
	upserted   []*model.Task
	removed    []string
	trigErr    error
	trigRun    *model.Run
	trigParams map[string]string
	triggered  []string

	startedServices   []string
	stoppedServices   []string
	restartedServices []string
	serviceErr        error
	snapshot          model.ServiceSnapshot
	snapshotOK        bool
}

func (f *fakeTaskRunner) GetTask(name string) (*model.Task, bool) {
	t, ok := f.tasks[name]
	return t, ok
}

func (f *fakeTaskRunner) ListServiceTasks() []*model.Task {
	out := make([]*model.Task, 0, len(f.tasks))
	for _, t := range f.tasks {
		if t != nil && t.Kind.IsService() {
			out = append(out, t)
		}
	}
	return out
}

func (f *fakeTaskRunner) UpsertTask(task *model.Task) {
	if f.tasks == nil {
		f.tasks = make(map[string]*model.Task)
	}
	f.tasks[task.Name] = task
	f.upserted = append(f.upserted, task)
}

func (f *fakeTaskRunner) RemoveTask(taskName string) {
	delete(f.tasks, taskName)
	f.removed = append(f.removed, taskName)
}

func (f *fakeTaskRunner) TriggerCloudRun(taskName, externalID string, params map[string]string) (*model.Run, error) {
	f.trigParams = params
	f.triggered = append(f.triggered, externalID)
	return f.trigRun, f.trigErr
}

func (f *fakeTaskRunner) TerminateRunByExecutionID(externalID string) error {
	return errors.New("not found")
}

func (f *fakeTaskRunner) StartServiceInstances(taskName string, _ model.TriggeredBy) error {
	f.startedServices = append(f.startedServices, taskName)
	return f.serviceErr
}

func (f *fakeTaskRunner) StopService(taskName string) error {
	f.stoppedServices = append(f.stoppedServices, taskName)
	return f.serviceErr
}

func (f *fakeTaskRunner) RestartServiceInstances(taskName string) error {
	f.restartedServices = append(f.restartedServices, taskName)
	return f.serviceErr
}

func (f *fakeTaskRunner) ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool) {
	return f.snapshot, f.snapshotOK
}

func newDispatchHandler(avail executor.Availability, tasks map[string]*model.Task) *InboundHandler {
	runner := &fakeTaskRunner{tasks: tasks}
	return &InboundHandler{
		taskManager:     runner,
		logDir:          "/tmp",
		availability:    avail,
		queueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		logListeners:    make(map[string]struct{}),
	}
}

func shellScript(t *testing.T, script string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"type": "shell", "script": script})
	require.NoError(t, err)
	return raw
}

func configScript(t *testing.T, taskName string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"type": "config", "taskName": taskName})
	require.NoError(t, err)
	return raw
}

func containerScript(t *testing.T, script string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"type": "container", "base_image": "alpine", "script": script})
	require.NoError(t, err)
	return raw
}

func httpScript(t *testing.T, url string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"type": "http", "method": "GET", "url": url})
	require.NoError(t, err)
	return raw
}

// --- resolveDispatchTask ---

func TestResolveDispatchTask_InvalidJSON(t *testing.T) {
	h := newDispatchHandler(executor.Availability{}, nil)
	_, _, err := h.resolveDispatchTask(&protocol.Execution{Script: json.RawMessage(`not-json`)})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestResolveDispatchTask_UnavailableBackend(t *testing.T) {
	avail := executor.Availability{
		Shell: executor.BackendStatus{Available: false, Reason: "no shell"},
	}
	h := newDispatchHandler(avail, nil)
	_, _, err := h.resolveDispatchTask(&protocol.Execution{Script: shellScript(t, "echo hi")})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestResolveDispatchTask_ConfigTaskFound(t *testing.T) {
	avail := executor.Availability{
		Config: executor.BackendStatus{Available: true},
	}
	tasks := map[string]*model.Task{"mytask": {Name: "mytask", ManualTrigger: true}}
	h := newDispatchHandler(avail, tasks)

	name, configBacked, err := h.resolveDispatchTask(&protocol.Execution{Script: configScript(t, "mytask")})
	require.NoError(t, err)
	assert.Equal(t, "mytask", name)
	assert.True(t, configBacked)
}

// TestResolveDispatchTask_ConfigTaskManualTriggerDisabled: manual_trigger=false means
// the task is schedule-only everywhere, so the control plane cannot trigger it
// either — mirroring the REST surface's ErrManualTriggerDisabled.
func TestResolveDispatchTask_ConfigTaskManualTriggerDisabled(t *testing.T) {
	avail := executor.Availability{
		Config: executor.BackendStatus{Available: true},
	}
	tasks := map[string]*model.Task{"mytask": {Name: "mytask", ManualTrigger: false}}
	h := newDispatchHandler(avail, tasks)

	_, _, err := h.resolveDispatchTask(&protocol.Execution{Script: configScript(t, "mytask")})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
	assert.Contains(t, ce.Message, "manual_trigger")
}

func TestResolveDispatchTask_ConfigTaskNotFound(t *testing.T) {
	avail := executor.Availability{
		Config: executor.BackendStatus{Available: true},
	}
	h := newDispatchHandler(avail, nil)

	_, _, err := h.resolveDispatchTask(&protocol.Execution{Script: configScript(t, "missing")})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestResolveDispatchTask_ShellInlineUpserted(t *testing.T) {
	avail := executor.Availability{
		Shell: executor.BackendStatus{Available: true},
	}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := &InboundHandler{
		taskManager:     runner,
		logDir:          "/tmp",
		availability:    avail,
		queueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		logListeners:    make(map[string]struct{}),
	}

	name, configBacked, err := h.resolveDispatchTask(&protocol.Execution{
		TaskID: "my-task",
		Script: shellScript(t, "echo hello"),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, name)
	assert.False(t, configBacked)
	assert.Len(t, runner.upserted, 1)
	assert.Equal(t, name, runner.upserted[0].Name)
}

// TestResolveDispatchTask_ContainerRejectedWhenDispatchDisabled covers the opt-in
// bypass fix: with cloud dispatch disabled, a container backend reports
// unavailable, so an ad-hoc container dispatch is rejected before any task is
// upserted.
func TestResolveDispatchTask_ContainerRejectedWhenDispatchDisabled(t *testing.T) {
	avail := executor.Availability{
		Container: executor.BackendStatus{Available: false, Reason: "cloud dispatch disabled (set [daemon] allow_cloud_dispatch = true to enable)"},
	}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := &InboundHandler{
		taskManager:     runner,
		logDir:          "/tmp",
		availability:    avail,
		queueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		logListeners:    make(map[string]struct{}),
	}

	_, _, err := h.resolveDispatchTask(&protocol.Execution{TaskID: "evil", Script: containerScript(t, "rm -rf /")})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
	assert.Empty(t, runner.upserted, "rejected dispatch must not upsert a task")
}

func TestResolveDispatchTask_ContainerInlineUpsertedWhenEnabled(t *testing.T) {
	avail := executor.Availability{
		Container: executor.BackendStatus{Available: true},
	}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := &InboundHandler{
		taskManager:     runner,
		logDir:          "/tmp",
		availability:    avail,
		queueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		logListeners:    make(map[string]struct{}),
	}

	name, configBacked, err := h.resolveDispatchTask(&protocol.Execution{TaskID: "build", Script: containerScript(t, "echo hi")})
	require.NoError(t, err)
	assert.NotEmpty(t, name)
	assert.False(t, configBacked)
	assert.Len(t, runner.upserted, 1)
}

// TestResolveDispatchTask_HTTPRejectedWithoutDispatch confirms HTTP-type
// dispatch is gated by allow_cloud_dispatch like shell/container/compose: it
// still makes a peer-directed network call, so it's not exempt from the opt-in.
func TestResolveDispatchTask_HTTPRejectedWithoutDispatch(t *testing.T) {
	avail := executor.Availability{
		HTTP: executor.BackendStatus{Available: false, Reason: "cloud dispatch disabled (set [daemon] allow_cloud_dispatch = true to enable)"},
	}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := &InboundHandler{
		taskManager:     runner,
		logDir:          "/tmp",
		availability:    avail,
		queueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		logListeners:    make(map[string]struct{}),
	}

	_, _, err := h.resolveDispatchTask(&protocol.Execution{TaskID: "probe", Script: httpScript(t, "https://example.com")})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
	assert.Empty(t, runner.upserted, "rejected dispatch must not upsert a task")
}

// TestResolveDispatchTask_HTTPAllowedWithDispatch confirms HTTP-type dispatch
// succeeds once the operator opts in via allow_cloud_dispatch.
func TestResolveDispatchTask_HTTPAllowedWithDispatch(t *testing.T) {
	avail := executor.Availability{
		HTTP: executor.BackendStatus{Available: true},
	}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := &InboundHandler{
		taskManager:     runner,
		logDir:          "/tmp",
		availability:    avail,
		queueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		logListeners:    make(map[string]struct{}),
	}

	name, configBacked, err := h.resolveDispatchTask(&protocol.Execution{TaskID: "probe", Script: httpScript(t, "https://example.com")})
	require.NoError(t, err)
	assert.NotEmpty(t, name)
	assert.False(t, configBacked)
}

// Regression: sanitizeCloudTaskName only prefixes "cloud-", so a peer picks the
// rest of the name and can aim an inline dispatch at a locally-defined task
// called cloud-*. Upserting over it would replace a disk-defined task's
// execution — and the ephemeral reaper would then delete the task outright — so
// the collision must be rejected. This test uses an HTTP dispatch with
// availability granted, so the rejection below is the name-collision guard, not
// the availability check exercised above.
func TestResolveDispatchTask_RejectsConfigTaskNameCollision(t *testing.T) {
	origExec := &model.ShellExecution{Script: "sync.sh"}
	local := &model.Task{Name: "cloud-sync", Kind: model.KindTask, Cron: "* * * * *", ExecutionDef: origExec}
	h := newDispatchHandler(executor.Availability{HTTP: executor.BackendStatus{Available: true}},
		map[string]*model.Task{"cloud-sync": local})
	runner := h.taskManager.(*fakeTaskRunner)

	_, _, err := h.resolveDispatchTask(&protocol.Execution{
		TaskID: "sync",
		Script: httpScript(t, "https://attacker.example/p"),
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
	assert.Empty(t, runner.upserted, "the local task definition must survive untouched")
	assert.Same(t, origExec, local.ExecutionDef)
}

// An inline dispatch reusing the name of an earlier inline dispatch is the
// normal retry case and must still upsert.
func TestResolveDispatchTask_EphemeralNameCollisionAllowed(t *testing.T) {
	prior := &model.Task{Name: "cloud-probe", Ephemeral: true, ExecutionDef: &model.ShellExecution{Script: "old"}}
	h := newDispatchHandler(executor.Availability{HTTP: executor.BackendStatus{Available: true}},
		map[string]*model.Task{"cloud-probe": prior})
	runner := h.taskManager.(*fakeTaskRunner)

	name, configBacked, err := h.resolveDispatchTask(&protocol.Execution{
		TaskID: "probe",
		Script: httpScript(t, "https://example.com"),
	})
	require.NoError(t, err)
	assert.Equal(t, "cloud-probe", name)
	assert.False(t, configBacked)
	assert.Len(t, runner.upserted, 1)
}

// --- buildDynamicCloudTask ---

func TestBuildDynamicCloudTask_UsesTaskID(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicCloudTask(&protocol.Execution{TaskID: "my-task"}, def)
	assert.Equal(t, "cloud-my-task", task.Name)
}

func TestBuildDynamicCloudTask_FallsBackToTaskName(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicCloudTask(&protocol.Execution{TaskName: "backup"}, def)
	assert.Equal(t, "cloud-backup", task.Name)
}

func TestBuildDynamicCloudTask_DefaultsToCloudInline(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicCloudTask(&protocol.Execution{}, def)
	assert.Equal(t, "cloud-inline", task.Name)
}

func TestBuildDynamicCloudTask_SetsTimeout(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicCloudTask(&protocol.Execution{TaskID: "t", Timeout: 5000}, def)
	assert.Equal(t, 5000*time.Millisecond, task.Timeout)
}

func TestBuildDynamicCloudTask_ZeroTimeoutIgnored(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicCloudTask(&protocol.Execution{TaskID: "t", Timeout: 0}, def)
	assert.Equal(t, time.Duration(0), task.Timeout)
}

func TestBuildDynamicCloudTask_AppliesTaskConfig(t *testing.T) {
	logOnFull := protocol.ExecutionTaskConfigLogOnFullDropOld
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicCloudTask(&protocol.Execution{
		TaskID: "t",
		TaskConfig: &protocol.ExecutionTaskConfig{
			Env:          map[string]string{"KEY": "val"},
			GracefulStop: 3000,
			LogMaxSize:   2048,
			LogOnFull:    &logOnFull,
		},
	}, def)

	assert.Equal(t, "val", task.Env["KEY"])
	assert.Equal(t, 3*time.Second, task.GracefulStop)
	assert.Equal(t, int64(2048), task.LogMaxSize)
	assert.Equal(t, "drop_old", task.LogOnFull)
}

func TestBuildDynamicCloudTask_NilTaskConfigLeavesDefaults(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicCloudTask(&protocol.Execution{TaskID: "t", TaskConfig: nil}, def)
	assert.Empty(t, task.Env)
	assert.Zero(t, task.GracefulStop)
	assert.Zero(t, task.LogMaxSize)
	assert.Empty(t, task.LogOnFull)
}

func TestSanitizeCloudTaskName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"plain name", "my-task", "cloud-my-task"},
		// SanitizeTaskName preserves case; spaces → underscores.
		{"spaces become underscores", "My Task", "cloud-My_Task"},
		{"leading and trailing spaces trimmed", "  hello  ", "cloud-hello"},
		// Trailing '!' is replaced by '_' then stripped by strings.Trim("-_").
		{"special chars replaced", "task@2024!", "cloud-task_2024"},
		{"already prefixed", "cloud-foo", "cloud-cloud-foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, sanitizeCloudTaskName(tt.input))
		})
	}
}
