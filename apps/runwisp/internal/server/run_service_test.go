// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
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

// helpers

func makeRunService(tasks map[string]*model.Task, repo *testutil.MockRunRepository, runner *mockTaskRunner) *runService {
	return newRunService(repo, runner, tasks, nil, "")
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

	_, err := svc.TriggerRun("missing")
	assert.ErrorIs(t, err, ErrTaskNotFound)
}

func TestTriggerRun_ServiceTask_ReturnsServiceNotRunnable(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"svc": {Name: "svc", Kind: model.KindService, APITrigger: true},
	}
	svc := makeRunService(tasks, repo, runner)

	_, err := svc.TriggerRun("svc")
	assert.ErrorIs(t, err, ErrServiceNotRunnable)
}

func TestTriggerRun_APITriggerDisabled(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"t": {Name: "t", Kind: model.KindTask, APITrigger: false},
	}
	svc := makeRunService(tasks, repo, runner)

	_, err := svc.TriggerRun("t")
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

	run, err := svc.TriggerRun("t")
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

	_, err := svc.TriggerRun("t")
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

	repo.On("GetRun", "run-999").Return(nil, storage.ErrNotFound)

	err := svc.StopRun("run-999")
	assert.ErrorIs(t, err, ErrRunNotFound)
	repo.AssertExpectations(t)
}

func TestStopRun_NotRunning(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhaseEnded}
	repo.On("GetRun", "run-1").Return(run, nil)

	err := svc.StopRun("run-1")
	assert.ErrorIs(t, err, ErrNotRunning)
	repo.AssertExpectations(t)
}

func TestStopRun_Success(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhaseRunning}
	repo.On("GetRun", "run-1").Return(run, nil)
	runner.On("TerminateRun", "run-1").Return(nil)

	err := svc.StopRun("run-1")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
	runner.AssertExpectations(t)
}

func TestStopRun_TerminateError(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhaseRunning}
	repo.On("GetRun", "run-1").Return(run, nil)

	termErr := errors.New("terminate failed")
	runner.On("TerminateRun", "run-1").Return(termErr)

	err := svc.StopRun("run-1")
	assert.ErrorIs(t, err, termErr)
	repo.AssertExpectations(t)
	runner.AssertExpectations(t)
}

func TestStopRun_RunPending_NotRunning(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(map[string]*model.Task{}, repo, runner)

	run := &model.Run{ID: "run-1", Status: model.PhasePending}
	repo.On("GetRun", "run-1").Return(run, nil)

	err := svc.StopRun("run-1")
	assert.ErrorIs(t, err, ErrNotRunning)
	repo.AssertExpectations(t)
}
