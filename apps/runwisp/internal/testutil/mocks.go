// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"context"
	"errors"
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

func (m *MockRunRepository) CountRunsFiltered(ctx context.Context, status, taskName, searchQuery string) (int64, error) {
	args := m.Called(ctx, status, taskName, searchQuery)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRunRepository) QueryRuns(ctx context.Context, taskName string, limit, offset int, status string, sortField storage.SortColumn, sortDirection storage.SortDirection, searchQuery string) ([]model.Run, error) {
	args := m.Called(ctx, taskName, limit, offset, status, sortField, sortDirection, searchQuery)
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
