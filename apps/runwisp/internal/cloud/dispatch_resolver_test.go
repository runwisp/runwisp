// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	tasks    map[string]*model.Task
	upserted []*model.Task
	trigErr  error
	trigRun  *model.Run
}

func (f *fakeTaskRunner) GetTask(name string) (*model.Task, bool) {
	t, ok := f.tasks[name]
	return t, ok
}

func (f *fakeTaskRunner) UpsertTask(task *model.Task) {
	if f.tasks == nil {
		f.tasks = make(map[string]*model.Task)
	}
	f.tasks[task.Name] = task
	f.upserted = append(f.upserted, task)
}

func (f *fakeTaskRunner) TriggerCloudRun(taskName, externalID string) (*model.Run, error) {
	return f.trigRun, f.trigErr
}

func (f *fakeTaskRunner) TerminateRunByExternalExecutionID(externalID string) error {
	return errors.New("not found")
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
	raw, err := json.Marshal(map[string]string{"type": "config", "task_name": taskName})
	require.NoError(t, err)
	return raw
}

// --- resolveDispatchTask ---

func TestResolveDispatchTask_InvalidJSON(t *testing.T) {
	h := newDispatchHandler(executor.Availability{}, nil)
	_, err := h.resolveDispatchTask(&protocol.Execution{Script: json.RawMessage(`not-json`)})
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
	_, err := h.resolveDispatchTask(&protocol.Execution{Script: shellScript(t, "echo hi")})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestResolveDispatchTask_ConfigTaskFound(t *testing.T) {
	avail := executor.Availability{
		Config: executor.BackendStatus{Available: true},
	}
	tasks := map[string]*model.Task{"mytask": {Name: "mytask"}}
	h := newDispatchHandler(avail, tasks)

	name, err := h.resolveDispatchTask(&protocol.Execution{Script: configScript(t, "mytask")})
	require.NoError(t, err)
	assert.Equal(t, "mytask", name)
}

func TestResolveDispatchTask_ConfigTaskNotFound(t *testing.T) {
	avail := executor.Availability{
		Config: executor.BackendStatus{Available: true},
	}
	h := newDispatchHandler(avail, nil)

	_, err := h.resolveDispatchTask(&protocol.Execution{Script: configScript(t, "missing")})
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

	name, err := h.resolveDispatchTask(&protocol.Execution{
		TaskID: "my-task",
		Script: shellScript(t, "echo hello"),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, name)
	assert.Len(t, runner.upserted, 1)
	assert.Equal(t, name, runner.upserted[0].Name)
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
