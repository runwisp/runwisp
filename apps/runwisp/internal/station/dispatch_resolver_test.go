// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

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

func (f *fakeTaskRunner) MutateTask(name string, mutate func(*model.Task) error) (bool, error) {
	t, ok := f.tasks[name]
	if !ok {
		return false, nil
	}
	taskCopy := *t
	if err := mutate(&taskCopy); err != nil {
		return true, err
	}
	if f.tasks == nil {
		f.tasks = make(map[string]*model.Task)
	}
	f.tasks[name] = &taskCopy
	f.upserted = append(f.upserted, &taskCopy)
	return true, nil
}

func (f *fakeTaskRunner) TriggerStationRun(taskName, externalID string, params map[string]string) (*model.Run, error) {
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
	var ce *StationError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, StationErrorKindValidation, ce.Kind)
}

func TestResolveDispatchTask_UnavailableBackend(t *testing.T) {
	avail := executor.Availability{
		Shell: executor.BackendStatus{Available: false, Reason: "no shell"},
	}
	h := newDispatchHandler(avail, nil)
	_, _, err := h.resolveDispatchTask(&protocol.Execution{Script: shellScript(t, "echo hi")})
	require.Error(t, err)
	var ce *StationError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, StationErrorKindConflict, ce.Kind)
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
	var ce *StationError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, StationErrorKindConflict, ce.Kind)
	assert.Contains(t, ce.Message, "manual_trigger")
}

// TestResolveDispatchTask_ConfigTaskIsService: a [services.*] entry is never
// manually triggered, regardless of ManualTrigger (which the config loader
// forces true internally since services can't set the key at all) — without
// this check the control plane could reserve it an extra instance.
func TestResolveDispatchTask_ConfigTaskIsService(t *testing.T) {
	avail := executor.Availability{
		Config: executor.BackendStatus{Available: true},
	}
	tasks := map[string]*model.Task{"myservice": {Name: "myservice", Kind: model.KindService, ManualTrigger: true}}
	h := newDispatchHandler(avail, tasks)

	_, _, err := h.resolveDispatchTask(&protocol.Execution{Script: configScript(t, "myservice")})
	require.Error(t, err)
	var ce *StationError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, StationErrorKindConflict, ce.Kind)
	assert.Contains(t, ce.Message, "service")
}

func TestResolveDispatchTask_ConfigTaskNotFound(t *testing.T) {
	avail := executor.Availability{
		Config: executor.BackendStatus{Available: true},
	}
	h := newDispatchHandler(avail, nil)

	_, _, err := h.resolveDispatchTask(&protocol.Execution{Script: configScript(t, "missing")})
	require.Error(t, err)
	var ce *StationError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, StationErrorKindConflict, ce.Kind)
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
// bypass fix: with station dispatch disabled, a container backend reports
// unavailable, so an ad-hoc container dispatch is rejected before any task is
// upserted.
func TestResolveDispatchTask_ContainerRejectedWhenDispatchDisabled(t *testing.T) {
	avail := executor.Availability{
		Container: executor.BackendStatus{Available: false, Reason: "station dispatch disabled (set [daemon] allow_station_dispatch = true to enable)"},
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
	var ce *StationError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, StationErrorKindConflict, ce.Kind)
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
// dispatch is gated by allow_station_dispatch like shell/container/compose: it
// still makes a peer-directed network call, so it's not exempt from the opt-in.
func TestResolveDispatchTask_HTTPRejectedWithoutDispatch(t *testing.T) {
	avail := executor.Availability{
		HTTP: executor.BackendStatus{Available: false, Reason: "station dispatch disabled (set [daemon] allow_station_dispatch = true to enable)"},
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
	var ce *StationError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, StationErrorKindConflict, ce.Kind)
	assert.Empty(t, runner.upserted, "rejected dispatch must not upsert a task")
}

// TestResolveDispatchTask_HTTPAllowedWithDispatch confirms HTTP-type dispatch
// succeeds once the operator opts in via allow_station_dispatch.
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

// Regression: sanitizeStationTaskName only prefixes "station-", so a peer picks the
// rest of the name and can aim an inline dispatch at a locally-defined task
// called station-*. Upserting over it would replace a disk-defined task's
// execution — and the ephemeral reaper would then delete the task outright — so
// the collision must be rejected. This test uses an HTTP dispatch with
// availability granted, so the rejection below is the name-collision guard, not
// the availability check exercised above.
func TestResolveDispatchTask_RejectsConfigTaskNameCollision(t *testing.T) {
	origExec := &model.ShellExecution{Script: "sync.sh"}
	local := &model.Task{Name: "station-sync", Kind: model.KindTask, Cron: "* * * * *", ExecutionDef: origExec}
	h := newDispatchHandler(executor.Availability{HTTP: executor.BackendStatus{Available: true}},
		map[string]*model.Task{"station-sync": local})
	runner := h.taskManager.(*fakeTaskRunner)

	_, _, err := h.resolveDispatchTask(&protocol.Execution{
		TaskID: "sync",
		Script: httpScript(t, "https://attacker.example/p"),
	})
	require.Error(t, err)
	var ce *StationError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, StationErrorKindConflict, ce.Kind)
	assert.Empty(t, runner.upserted, "the local task definition must survive untouched")
	assert.Same(t, origExec, local.ExecutionDef)
}

// An inline dispatch reusing the name of an earlier inline dispatch is the
// normal retry case and must still upsert.
func TestResolveDispatchTask_EphemeralNameCollisionAllowed(t *testing.T) {
	prior := &model.Task{Name: "station-probe", Ephemeral: true, ExecutionDef: &model.ShellExecution{Script: "old"}}
	h := newDispatchHandler(executor.Availability{HTTP: executor.BackendStatus{Available: true}},
		map[string]*model.Task{"station-probe": prior})
	runner := h.taskManager.(*fakeTaskRunner)

	name, configBacked, err := h.resolveDispatchTask(&protocol.Execution{
		TaskID: "probe",
		Script: httpScript(t, "https://example.com"),
	})
	require.NoError(t, err)
	assert.Equal(t, "station-probe", name)
	assert.False(t, configBacked)
	assert.Len(t, runner.upserted, 1)
}

// --- buildDynamicStationTask ---

func TestBuildDynamicStationTask_UsesTaskID(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicStationTask(&protocol.Execution{TaskID: "my-task"}, def)
	assert.Equal(t, "station-my-task", task.Name)
}

func TestBuildDynamicStationTask_FallsBackToTaskName(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicStationTask(&protocol.Execution{TaskName: "backup"}, def)
	assert.Equal(t, "station-backup", task.Name)
}

func TestBuildDynamicStationTask_DefaultsToStationInline(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicStationTask(&protocol.Execution{}, def)
	assert.Equal(t, "station-inline", task.Name)
}

func TestBuildDynamicStationTask_SetsTimeout(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicStationTask(&protocol.Execution{TaskID: "t", Timeout: 5000}, def)
	assert.Equal(t, 5000*time.Millisecond, task.TimeoutValue())
}

func TestBuildDynamicStationTask_ZeroTimeoutIgnored(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicStationTask(&protocol.Execution{TaskID: "t", Timeout: 0}, def)
	assert.Nil(t, task.Timeout)
	assert.Equal(t, time.Duration(0), task.TimeoutValue())
}

func TestBuildDynamicStationTask_AppliesTaskConfig(t *testing.T) {
	logOnFull := protocol.ExecutionTaskConfigLogOnFullDropOld
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicStationTask(&protocol.Execution{
		TaskID: "t",
		TaskConfig: &protocol.ExecutionTaskConfig{
			Env:          map[string]string{"KEY": "val"},
			GracefulStop: 3000,
			LogMaxSize:   2048,
			LogOnFull:    &logOnFull,
		},
	}, def)

	assert.Equal(t, "val", task.Env["KEY"])
	assert.Equal(t, 3*time.Second, task.GracefulStopValue())
	assert.Equal(t, int64(2048), task.LogMaxSize)
	assert.Equal(t, "drop_old", task.LogOnFull)
}

func TestBuildDynamicStationTask_NilTaskConfigLeavesDefaults(t *testing.T) {
	def := &model.ShellExecution{Script: "echo hi"}
	task := buildDynamicStationTask(&protocol.Execution{TaskID: "t", TaskConfig: nil}, def)
	assert.Empty(t, task.Env)
	assert.Zero(t, task.GracefulStopValue())
	assert.Zero(t, task.LogMaxSize)
	assert.Empty(t, task.LogOnFull)
}

func TestSanitizeStationTaskName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"plain name", "my-task", "station-my-task"},
		// SanitizeTaskName preserves case; spaces → underscores.
		{"spaces become underscores", "My Task", "station-My_Task"},
		{"leading and trailing spaces trimmed", "  hello  ", "station-hello"},
		// Trailing '!' is replaced by '_' then stripped by strings.Trim("-_").
		{"special chars replaced", "task@2024!", "station-task_2024"},
		{"already prefixed", "station-foo", "station-station-foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, sanitizeStationTaskName(tt.input))
		})
	}
}
