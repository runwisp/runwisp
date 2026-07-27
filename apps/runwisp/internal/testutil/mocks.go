// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/mock"
)

// MockRunRepository is a testify mock for storage.RunRepository.
type MockRunRepository struct {
	mock.Mock
}

func (m *MockRunRepository) CreateRun(ctx context.Context, run *model.Run) error {
	args := m.Called(ctx, run)
	return args.Error(0)
}

func (m *MockRunRepository) UpdateRun(ctx context.Context, run *model.Run) error {
	args := m.MethodCalled("UpdateRun", ctx, run)
	return args.Error(0)
}

func (m *MockRunRepository) GetRun(ctx context.Context, id string) (*model.Run, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Run), args.Error(1)
}

func (m *MockRunRepository) GetRunByExternalExecutionID(ctx context.Context, externalExecutionID string) (*model.Run, error) {
	args := m.Called(ctx, externalExecutionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Run), args.Error(1)
}

func (m *MockRunRepository) CountRuns(ctx context.Context, taskName string) (int64, error) {
	args := m.Called(ctx, taskName)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRunRepository) CountRunsFiltered(ctx context.Context, filter model.RunFilter) (int64, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRunRepository) QueryRuns(ctx context.Context, q storage.RunQuery) ([]model.Run, error) {
	args := m.Called(ctx, q)
	return args.Get(0).([]model.Run), args.Error(1)
}

func (m *MockRunRepository) DeleteRun(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockRunRepository) DeleteOldRuns(ctx context.Context, task *model.Task) ([]model.Run, error) {
	args := m.Called(ctx, task)
	return args.Get(0).([]model.Run), args.Error(1)
}

func (m *MockRunRepository) MarkCrashedRuns(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRunRepository) GetPendingRuns(ctx context.Context) ([]model.Run, error) {
	args := m.Called(ctx)
	return args.Get(0).([]model.Run), args.Error(1)
}

func (m *MockRunRepository) GetLastRunByTask(ctx context.Context, taskName string) (*model.Run, error) {
	args := m.Called(ctx, taskName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.Run), args.Error(1)
}

func (m *MockRunRepository) GetRunSummary(ctx context.Context) (*model.RunSummary, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.RunSummary), args.Error(1)
}

func (m *MockRunRepository) EnsureTaskRegistered(ctx context.Context, taskName string, firstSeen time.Time) error {
	args := m.Called(ctx, taskName, firstSeen)
	return args.Error(0)
}

func (m *MockRunRepository) GetTaskRegistration(ctx context.Context, taskName string) (*model.TaskRegistration, error) {
	args := m.Called(ctx, taskName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.TaskRegistration), args.Error(1)
}

func (m *MockRunRepository) SoftDeleteRuns(ctx context.Context, sel model.RunSelector, deletedAt time.Time) ([]storage.RunRef, error) {
	args := m.Called(ctx, sel, deletedAt)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]storage.RunRef), args.Error(1)
}

func (m *MockRunRepository) RestoreRuns(ctx context.Context, sel model.RunSelector) ([]model.Run, error) {
	args := m.Called(ctx, sel)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.Run), args.Error(1)
}

func (m *MockRunRepository) ResolveSelectorIDs(ctx context.Context, sel model.RunSelector, statusFilter string) ([]storage.RunRef, error) {
	args := m.Called(ctx, sel, statusFilter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]storage.RunRef), args.Error(1)
}

func (m *MockRunRepository) PurgeExpiredSoftDeletes(ctx context.Context, ttl time.Duration) ([]storage.RunRef, error) {
	args := m.Called(ctx, ttl)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]storage.RunRef), args.Error(1)
}

func (m *MockRunRepository) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockExecutor is a testify mock for executor.Executor.
type MockExecutor struct {
	mock.Mock
}

func (m *MockExecutor) Execute(ctx context.Context, task *model.Task, run *model.Run) *executor.ExecuteResult {
	args := m.Called(ctx, task, run)

	if len(args) > 1 {
		if d, ok := args.Get(1).(time.Duration); ok {
			select {
			case <-ctx.Done():
				return cancelledResult(ctx.Err())
			case <-time.After(d):
			}
		}
	} else {
		select {
		case <-ctx.Done():
			return cancelledResult(ctx.Err())
		default:
		}
	}

	return args.Get(0).(*executor.ExecuteResult)
}

// cancelledResult mirrors the real executor's classification of a cancelled
// context: DeadlineExceeded → TimedOut, Canceled → Stopped.
func cancelledResult(err error) *executor.ExecuteResult {
	return &executor.ExecuteResult{
		ExitCode: -1,
		Error:    err,
		TimedOut: errors.Is(err, context.DeadlineExceeded),
		Stopped:  errors.Is(err, context.Canceled),
	}
}

func (m *MockExecutor) Availability() executor.Availability {
	return executor.Availability{}
}

// GateExecutor is a deterministic executor.Executor for concurrency tests.
// Each Execute call announces its run ID on a channel and then blocks until
// the test releases it (or the run's context is cancelled). This lets a test
// hold a run "in flight" while it triggers overlapping runs and asserts on
// concurrency policy — with no sleeps to guess at timing.
type GateExecutor struct {
	// Result is returned when Execute is released normally. Defaults to a clean
	// exit-0 result when nil.
	Result *executor.ExecuteResult

	started chan string
	gate    chan struct{}
	calls   atomic.Int32
}

func NewGateExecutor() *GateExecutor {
	return &GateExecutor{
		started: make(chan string, 64),
		gate:    make(chan struct{}),
	}
}

func (g *GateExecutor) Execute(ctx context.Context, _ *model.Task, run *model.Run) *executor.ExecuteResult {
	g.calls.Add(1)
	// The started-send must itself honour cancellation: if a test produces more
	// starts than the buffer holds without draining them, an unconditional send
	// would block here forever — immune to ctx cancellation — and hang the
	// manager's shutdown drain. Selecting on ctx.Done() keeps the run killable.
	select {
	case g.started <- run.ID:
	case <-ctx.Done():
		return cancelledResult(ctx.Err())
	}
	select {
	case <-g.gate:
		if g.Result != nil {
			return g.Result
		}
		return &executor.ExecuteResult{ExitCode: 0}
	case <-ctx.Done():
		return cancelledResult(ctx.Err())
	}
}

func (g *GateExecutor) Availability() executor.Availability { return executor.Availability{} }

// WaitStarted blocks until a run reports it has begun executing, returning its
// ID. It fails the test on timeout so a wiring bug surfaces as a clear failure
// rather than a hang.
func (g *GateExecutor) WaitStarted(t *testing.T) string {
	t.Helper()
	select {
	case id := <-g.started:
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a run to start executing")
		return ""
	}
}

// ReleaseAll unblocks every in-flight and future Execute call. Idempotent
// only once — call it a single time per executor.
func (g *GateExecutor) ReleaseAll() { close(g.gate) }

// Calls reports how many times Execute has been invoked.
func (g *GateExecutor) Calls() int { return int(g.calls.Load()) }
