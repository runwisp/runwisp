// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/redact"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockTaskRunner is a minimal mock for runtime.TaskRunner.
type mockTaskRunner struct {
	mock.Mock
}

func (m *mockTaskRunner) TriggerRun(taskName string, triggeredBy model.TriggeredBy) (*model.Run, error) {
	args := m.Called(taskName, triggeredBy)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Run), args.Error(1)
}

func (m *mockTaskRunner) TriggerRunWithOptions(taskName string, options runtime.TriggerRunOptions) (*model.Run, error) {
	args := m.Called(taskName, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Run), args.Error(1)
}

func (m *mockTaskRunner) GetTask(taskName string) (*model.Task, bool) {
	args := m.Called(taskName)
	if args.Get(0) == nil {
		return nil, args.Bool(1)
	}
	return args.Get(0).(*model.Task), args.Bool(1)
}

func (m *mockTaskRunner) UpsertTask(task *model.Task) {
	m.Called(task)
}

func (m *mockTaskRunner) TerminateRun(runID string) error {
	args := m.MethodCalled("TerminateRun", runID)
	return args.Error(0)
}

func (m *mockTaskRunner) TerminateRunByExternalExecutionID(externalExecutionID string) error {
	args := m.MethodCalled("TerminateRunByExternalExecutionID", externalExecutionID)
	return args.Error(0)
}

func (m *mockTaskRunner) RestartServiceInstances(taskName string) error {
	args := m.MethodCalled("RestartServiceInstances", taskName)
	return args.Error(0)
}

func (m *mockTaskRunner) StopService(taskName string) error {
	args := m.MethodCalled("StopService", taskName)
	return args.Error(0)
}

func (m *mockTaskRunner) RecordSkippedFiring(taskName string, reason model.EndReason, triggeredBy model.TriggeredBy) error {
	args := m.Called(taskName, reason, triggeredBy)
	return args.Error(0)
}

func (m *mockTaskRunner) GetActiveRunCount(taskName string) int {
	args := m.Called(taskName)
	return args.Int(0)
}

// helpers

func makeRunService(tasks map[string]*model.Task, repo *testutil.MockRunRepository, runner *mockTaskRunner) *runService {
	return newRunService(repo, runner, tasks, nil, "", nil, nil, "", nil)
}

// ---- ListTasks secret view ----

func TestListTasks_RevealAndRedactFreeFormFields(t *testing.T) {
	t.Setenv("RW_SRV_HIDDEN", "hidden-val-abc")
	t.Setenv("RW_SRV_REVEAL", "shown-val-xyz")

	task := &model.Task{
		Name:        "build",
		Group:       "${RW_SRV_HIDDEN}",
		Description: "deploy ${RW_SRV_REVEAL}",
		Env: map[string]string{
			"SECRET": "${RW_SRV_HIDDEN}",
			"SHOWN":  "${RW_SRV_REVEAL}",
			"PLAIN":  "literal",
		},
	}
	tasks := map[string]*model.Task{"build": task}

	red := redact.New()
	red.Add("hidden-val-abc") // the boot redactor would seed this hidden value
	reveal := map[string]bool{"RW_SRV_REVEAL": true}

	svc := newRunService(nil, nil, tasks, nil, "", nil, reveal, "", red)
	got := svc.ListTasks()
	require.Len(t, got, 1)
	dto := got[0]

	// Unrevealed → raw placeholder kept; revealed → resolved value shown.
	assert.Equal(t, "${RW_SRV_HIDDEN}", dto.Env["SECRET"], "unrevealed var stays a placeholder")
	assert.Equal(t, "shown-val-xyz", dto.Env["SHOWN"], "revealed var resolves")
	assert.Equal(t, "literal", dto.Env["PLAIN"])
	assert.Equal(t, "${RW_SRV_HIDDEN}", dto.Group, "unrevealed group stays a placeholder")
	assert.Equal(t, "deploy shown-val-xyz", dto.Description, "revealed var resolves in description")

	// The in-memory task is never mutated by the DTO transform.
	assert.Equal(t, "${RW_SRV_HIDDEN}", task.Env["SECRET"], "source task keeps the raw placeholder")
	assert.Equal(t, "deploy ${RW_SRV_REVEAL}", task.Description)
}

func TestListTasks_RedactorMasksHiddenValueInDTO(t *testing.T) {
	// A free-form field that happens to contain a hidden secret value verbatim
	// (no placeholder) is still masked by the content backstop.
	task := &model.Task{
		Name:        "leaky",
		Description: "token is hidden-val-abc do not show",
	}
	red := redact.New()
	red.Add("hidden-val-abc")

	svc := newRunService(nil, nil, map[string]*model.Task{"leaky": task}, nil, "", nil, nil, "", red)
	got := svc.ListTasks()
	require.Len(t, got, 1)
	assert.Equal(t, "token is [redacted] do not show", got[0].Description)
}

// ---- mapNotFound ----

func TestMapNotFound_TranslatesStorageErrNotFound(t *testing.T) {
	err := mapNotFound(storage.ErrNotFound)
	assert.ErrorIs(t, err, ErrRunNotFound)
}

func TestMapNotFound_PassesThroughOtherErrors(t *testing.T) {
	other := errors.New("some other error")
	err := mapNotFound(other)
	assert.Equal(t, other, err)
}

func TestMapNotFound_NilPassesThrough(t *testing.T) {
	err := mapNotFound(nil)
	assert.NoError(t, err)
}

// ---- TriggerRun ----

func TestTriggerRun_TaskNotFound(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	_, err := svc.TriggerRun(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrTaskNotFound)
}

func TestTriggerRun_ServiceTask_ReturnsServiceNotRunnable(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"svc": {Name: "svc", Kind: model.KindService, APITrigger: true},
	}
	svc := makeRunService(tasks, repo, runner)

	_, err := svc.TriggerRun(context.Background(), "svc")
	assert.ErrorIs(t, err, ErrServiceNotRunnable)
}

func TestTriggerRun_APITriggerDisabled(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"t": {Name: "t", Kind: model.KindTask, APITrigger: false},
	}
	svc := makeRunService(tasks, repo, runner)

	_, err := svc.TriggerRun(context.Background(), "t")
	assert.ErrorIs(t, err, ErrAPIDisabled)
}

func TestTriggerRun_Success(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"t": {Name: "t", Kind: model.KindTask, APITrigger: true},
	}
	svc := makeRunService(tasks, repo, runner)

	expected := &model.Run{ID: "run-1", TaskName: "t"}
	runner.On("TriggerRun", "t", model.TriggeredByAPI).Return(expected, nil)

	run, err := svc.TriggerRun(context.Background(), "t")
	require.NoError(t, err)
	assert.Equal(t, expected, run)
	runner.AssertExpectations(t)
}

func TestTriggerRun_RunnerError(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"t": {Name: "t", Kind: model.KindTask, APITrigger: true},
	}
	svc := makeRunService(tasks, repo, runner)

	runnerErr := errors.New("runner failed")
	runner.On("TriggerRun", "t", model.TriggeredByAPI).Return(nil, runnerErr)

	_, err := svc.TriggerRun(context.Background(), "t")
	assert.ErrorIs(t, err, runnerErr)
	runner.AssertExpectations(t)
}

// ---- RestartService ----

func TestRestartService_TaskNotFound(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	err := svc.RestartService("missing")
	assert.ErrorIs(t, err, ErrTaskNotFound)
}

func TestRestartService_NotAService(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"t": {Name: "t", Kind: model.KindTask},
	}
	svc := makeRunService(tasks, repo, runner)

	err := svc.RestartService("t")
	assert.ErrorIs(t, err, ErrNotAService)
}

func TestRestartService_Success(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"svc": {Name: "svc", Kind: model.KindService},
	}
	svc := makeRunService(tasks, repo, runner)

	runner.On("RestartServiceInstances", "svc").Return(nil)

	err := svc.RestartService("svc")
	assert.NoError(t, err)
	runner.AssertExpectations(t)
}

func TestRestartService_RunnerError(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"svc": {Name: "svc", Kind: model.KindService},
	}
	svc := makeRunService(tasks, repo, runner)

	restartErr := errors.New("restart failed")
	runner.On("RestartServiceInstances", "svc").Return(restartErr)

	err := svc.RestartService("svc")
	assert.ErrorIs(t, err, restartErr)
	runner.AssertExpectations(t)
}

// ---- StopService ----

func TestStopService_TaskNotFound(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	err := svc.StopService("missing")
	assert.ErrorIs(t, err, ErrTaskNotFound)
}

func TestStopService_NotAService(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"t": {Name: "t", Kind: model.KindTask},
	}
	svc := makeRunService(tasks, repo, runner)

	err := svc.StopService("t")
	assert.ErrorIs(t, err, ErrNotAService)
}

func TestStopService_Success(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"svc": {Name: "svc", Kind: model.KindService},
	}
	svc := makeRunService(tasks, repo, runner)

	runner.On("StopService", "svc").Return(nil)

	err := svc.StopService("svc")
	assert.NoError(t, err)
	runner.AssertExpectations(t)
}

func TestStopService_RunnerError(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"svc": {Name: "svc", Kind: model.KindService},
	}
	svc := makeRunService(tasks, repo, runner)

	stopErr := errors.New("stop failed")
	runner.On("StopService", "svc").Return(stopErr)

	err := svc.StopService("svc")
	assert.ErrorIs(t, err, stopErr)
	runner.AssertExpectations(t)
}

// ---- StopRun ----

func TestStopRun_RunNotFound(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	repo.On("GetRun", mock.Anything, "run-999").Return(nil, storage.ErrNotFound)

	err := svc.StopRun(context.Background(), "run-999")
	assert.ErrorIs(t, err, ErrRunNotFound)
	repo.AssertExpectations(t)
}

func TestStopRun_NotRunning(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhaseEnded}
	repo.On("GetRun", mock.Anything, "run-1").Return(run, nil)

	err := svc.StopRun(context.Background(), "run-1")
	assert.ErrorIs(t, err, ErrNotRunning)
	repo.AssertExpectations(t)
}

func TestStopRun_Success(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhaseRunning}
	repo.On("GetRun", mock.Anything, "run-1").Return(run, nil)
	runner.On("TerminateRun", "run-1").Return(nil)

	err := svc.StopRun(context.Background(), "run-1")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
	runner.AssertExpectations(t)
}

func TestStopRun_TerminateError(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhaseRunning}
	repo.On("GetRun", mock.Anything, "run-1").Return(run, nil)

	termErr := errors.New("terminate failed")
	runner.On("TerminateRun", "run-1").Return(termErr)

	err := svc.StopRun(context.Background(), "run-1")
	assert.ErrorIs(t, err, termErr)
	repo.AssertExpectations(t)
	runner.AssertExpectations(t)
}

// ---- DeleteRun ----

func TestDeleteRun_RunNotFound(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	repo.On("GetRun", mock.Anything, "missing").Return(nil, storage.ErrNotFound)

	err := svc.DeleteRun(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrRunNotFound)
	repo.AssertExpectations(t)
}

func TestDeleteRun_RunningRun_RejectsWithConflict(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhaseRunning}
	repo.On("GetRun", mock.Anything, "run-1").Return(run, nil)

	err := svc.DeleteRun(context.Background(), "run-1")
	assert.ErrorIs(t, err, ErrCannotDeleteActiveRun)
	repo.AssertExpectations(t)
	// DeleteRun must not be invoked when the run is active.
	repo.AssertNotCalled(t, "DeleteRun", mock.Anything)
}

func TestDeleteRun_PendingRun_RejectsWithConflict(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-2", Status: model.PhasePending}
	repo.On("GetRun", mock.Anything, "run-2").Return(run, nil)

	err := svc.DeleteRun(context.Background(), "run-2")
	assert.ErrorIs(t, err, ErrCannotDeleteActiveRun)
	repo.AssertExpectations(t)
	repo.AssertNotCalled(t, "DeleteRun", mock.Anything)
}

func TestDeleteRun_EndedRun_Succeeds(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-3", TaskName: "t", Status: model.PhaseEnded}
	repo.On("GetRun", mock.Anything, "run-3").Return(run, nil)
	repo.On("SoftDeleteRuns", mock.Anything, mock.MatchedBy(func(sel model.RunSelector) bool {
		return !sel.MatchAll && len(sel.IDs) == 1 && sel.IDs[0] == "run-3"
	}), mock.Anything).Return([]storage.RunRef{{ID: "run-3", TaskName: "t"}}, nil)

	err := svc.DeleteRun(context.Background(), "run-3")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestStopRun_RunPending_NotRunning(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhasePending}
	repo.On("GetRun", mock.Anything, "run-1").Return(run, nil)

	err := svc.StopRun(context.Background(), "run-1")
	assert.ErrorIs(t, err, ErrNotRunning)
	repo.AssertExpectations(t)
}
