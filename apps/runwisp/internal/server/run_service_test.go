// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
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

func (m *mockTaskRunner) GetActiveRunCount(taskName string) int {
	args := m.Called(taskName)
	return args.Int(0)
}

// helpers

func makeRunService(tasks map[string]*model.Task, repo *testutil.MockRunRepository, runner *mockTaskRunner) *runService {
	return newRunService(repo, runner, tasks, nil, "", nil)
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

// ---- bulkCancel ----

func TestBulkCancel_InvalidSelector(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(nil, repo, runner)

	// Both IDs and MatchAll set — invalid.
	_, err := svc.bulkCancel(t.Context(), model.RunSelector{MatchAll: true, IDs: []string{"a"}})
	assert.Error(t, err)
}

func TestBulkCancel_TerminatesEachResolvedRun(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(nil, repo, runner)

	sel := model.RunSelector{IDs: []string{"a", "b", "c"}}
	repo.On("ResolveSelectorIDs", mock.Anything, sel, string(model.PhaseRunning)).Return(
		[]storage.RunRef{
			{ID: "a", TaskName: "t1"},
			{ID: "b", TaskName: "t1"},
			{ID: "c", TaskName: "t2"},
		}, nil)
	runner.On("TerminateRun", "a").Return(nil)
	runner.On("TerminateRun", "b").Return(errors.New("already gone"))
	runner.On("TerminateRun", "c").Return(nil)

	signalled, err := svc.bulkCancel(t.Context(), sel)
	require.NoError(t, err)
	// Only two succeeded; the middle one returned an error.
	assert.Equal(t, 2, signalled)
	repo.AssertExpectations(t)
	runner.AssertExpectations(t)
}

func TestBulkCancel_ResolveError(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(nil, repo, runner)

	sel := model.RunSelector{IDs: []string{"a"}}
	repo.On("ResolveSelectorIDs", mock.Anything, sel, string(model.PhaseRunning)).Return(nil, errors.New("db boom"))

	_, err := svc.bulkCancel(t.Context(), sel)
	assert.Error(t, err)
	repo.AssertExpectations(t)
}

// ---- bulkRerun ----

func TestBulkRerun_InvalidSelector(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(nil, repo, runner)

	_, err := svc.bulkRerun(t.Context(), model.RunSelector{}) // empty IDs + not MatchAll
	assert.Error(t, err)
}

func TestBulkRerun_TriggersOnePerTaskAndDedupes(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"alpha": {Name: "alpha", APITrigger: true, Kind: model.KindTask},
		"beta":  {Name: "beta", APITrigger: true, Kind: model.KindTask},
		"svc":   {Name: "svc", APITrigger: true, Kind: model.KindService},
		"noapi": {Name: "noapi", APITrigger: false, Kind: model.KindTask},
	}
	svc := makeRunService(tasks, repo, runner)

	sel := model.RunSelector{IDs: []string{"r1", "r2", "r3", "r4", "r5"}}
	repo.On("ResolveSelectorIDs", mock.Anything, sel, "").Return([]storage.RunRef{
		{ID: "r1", TaskName: "alpha"},
		{ID: "r2", TaskName: "alpha"},
		{ID: "r3", TaskName: "beta"},
		{ID: "r4", TaskName: "svc"},   // service task — should be skipped
		{ID: "r5", TaskName: "noapi"}, // APITrigger=false — skipped
	}, nil)
	runner.On("TriggerRun", "alpha", model.TriggeredByAPI).Return(&model.Run{ID: "newA"}, nil)
	runner.On("TriggerRun", "beta", model.TriggeredByAPI).Return(&model.Run{ID: "newB"}, nil)

	out, err := svc.bulkRerun(t.Context(), sel)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "alpha", out[0].TaskName)
	assert.Equal(t, "newA", out[0].RunID)
	assert.Equal(t, "beta", out[1].TaskName)
	assert.Equal(t, "newB", out[1].RunID)
	repo.AssertExpectations(t)
	runner.AssertExpectations(t)
}

func TestBulkRerun_TriggerErrorIsSkipped(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"alpha": {Name: "alpha", APITrigger: true, Kind: model.KindTask},
	}
	svc := makeRunService(tasks, repo, runner)

	sel := model.RunSelector{IDs: []string{"r1"}}
	repo.On("ResolveSelectorIDs", mock.Anything, sel, "").Return([]storage.RunRef{
		{ID: "r1", TaskName: "alpha"},
	}, nil)
	runner.On("TriggerRun", "alpha", model.TriggeredByAPI).Return(nil, errors.New("trigger boom"))

	out, err := svc.bulkRerun(t.Context(), sel)
	require.NoError(t, err)
	assert.Empty(t, out)
}

// ---- huma bulk handlers ----

func TestHumaBulkCancelRuns_SignalsTroughService(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(nil, repo, runner)

	sel := model.RunSelector{IDs: []string{"r1"}}
	repo.On("ResolveSelectorIDs", mock.Anything, sel, string(model.PhaseRunning)).Return(
		[]storage.RunRef{{ID: "r1", TaskName: "t"}}, nil)
	runner.On("TerminateRun", "r1").Return(nil)

	srv := &Server{runService: svc}
	out, err := srv.humaBulkCancelRuns(context.Background(), &BulkRunSelectorInput{Body: sel})
	require.NoError(t, err)
	assert.Equal(t, 1, out.Body.Affected)
}

func TestHumaBulkCancelRuns_InvalidSelectorReturnsError(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(nil, repo, runner)
	srv := &Server{runService: svc}

	_, err := srv.humaBulkCancelRuns(context.Background(),
		&BulkRunSelectorInput{Body: model.RunSelector{MatchAll: true, IDs: []string{"x"}}})
	assert.Error(t, err)
}

func TestHumaBulkRerunRuns_ReturnsTriggeredList(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	tasks := map[string]*model.Task{
		"alpha": {Name: "alpha", APITrigger: true, Kind: model.KindTask},
	}
	svc := makeRunService(tasks, repo, runner)

	sel := model.RunSelector{IDs: []string{"r1"}}
	repo.On("ResolveSelectorIDs", mock.Anything, sel, "").Return(
		[]storage.RunRef{{ID: "r1", TaskName: "alpha"}}, nil)
	runner.On("TriggerRun", "alpha", model.TriggeredByAPI).Return(&model.Run{ID: "newA"}, nil)

	srv := &Server{runService: svc}
	out, err := srv.humaBulkRerunRuns(context.Background(), &BulkRunSelectorInput{Body: sel})
	require.NoError(t, err)
	require.Len(t, out.Body.Triggered, 1)
	assert.Equal(t, "alpha", out.Body.Triggered[0].TaskName)
	assert.Equal(t, "newA", out.Body.Triggered[0].RunID)
}

func TestHumaBulkRerunRuns_InvalidSelectorReturnsError(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	svc := makeRunService(nil, repo, runner)
	srv := &Server{runService: svc}

	_, err := srv.humaBulkRerunRuns(context.Background(),
		&BulkRunSelectorInput{Body: model.RunSelector{}})
	assert.Error(t, err)
}
