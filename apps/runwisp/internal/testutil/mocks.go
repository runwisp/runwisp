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
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/stretchr/testify/mock"
)

// MockRunRepository is a testify mock for storage.RunRepository.
type MockRunRepository struct {
	mock.Mock
}

func (m *MockRunRepository) CreateRun(run *sqlcdb.Run) error {
	args := m.Called(run)
	return args.Error(0)
}

func (m *MockRunRepository) UpdateRun(run *sqlcdb.Run) error {
	args := m.MethodCalled("UpdateRun", run)
	return args.Error(0)
}

func (m *MockRunRepository) GetRun(id string) (*sqlcdb.Run, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sqlcdb.Run), args.Error(1)
}

func (m *MockRunRepository) GetRunByExternalExecutionID(externalExecutionID string) (*sqlcdb.Run, error) {
	args := m.Called(externalExecutionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sqlcdb.Run), args.Error(1)
}

func (m *MockRunRepository) CountRuns(taskName string) (int64, error) {
	args := m.Called(taskName)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRunRepository) CountRunsFiltered(status, taskName, searchQuery string) (int64, error) {
	args := m.Called(status, taskName, searchQuery)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRunRepository) QueryRuns(taskName string, limit, offset int, status string, sortField storage.SortColumn, sortDirection storage.SortDirection, searchQuery string) ([]sqlcdb.Run, error) {
	args := m.Called(taskName, limit, offset, status, sortField, sortDirection, searchQuery)
	return args.Get(0).([]sqlcdb.Run), args.Error(1)
}

func (m *MockRunRepository) DeleteRun(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockRunRepository) DeleteOldRuns(task *model.Task) ([]sqlcdb.Run, error) {
	args := m.Called(task)
	return args.Get(0).([]sqlcdb.Run), args.Error(1)
}

func (m *MockRunRepository) MarkCrashedRuns() (int64, error) {
	args := m.Called()
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockRunRepository) GetPendingRuns() ([]sqlcdb.Run, error) {
	args := m.Called()
	return args.Get(0).([]sqlcdb.Run), args.Error(1)
}

func (m *MockRunRepository) GetLastRunByTask(taskName string) (*sqlcdb.Run, error) {
	args := m.Called(taskName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sqlcdb.Run), args.Error(1)
}

func (m *MockRunRepository) GetRunSummary() (*sqlcdb.RunSummary, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*sqlcdb.RunSummary), args.Error(1)
}

func (m *MockRunRepository) EnsureTaskRegistered(taskName string, firstSeen time.Time) error {
	args := m.Called(taskName, firstSeen)
	return args.Error(0)
}

func (m *MockRunRepository) GetTaskRegistration(taskName string) (*model.TaskRegistration, error) {
	args := m.Called(taskName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*model.TaskRegistration), args.Error(1)
}

func (m *MockRunRepository) SoftDeleteRuns(sel model.RunSelector, deletedAt time.Time) ([]storage.RunRef, error) {
	args := m.Called(sel, deletedAt)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]storage.RunRef), args.Error(1)
}

func (m *MockRunRepository) RestoreRuns(sel model.RunSelector) ([]sqlcdb.Run, error) {
	args := m.Called(sel)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]sqlcdb.Run), args.Error(1)
}

func (m *MockRunRepository) ResolveSelectorIDs(sel model.RunSelector, statusFilter string) ([]storage.RunRef, error) {
	args := m.Called(sel, statusFilter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]storage.RunRef), args.Error(1)
}

func (m *MockRunRepository) PurgeExpiredSoftDeletes(ttl time.Duration) ([]storage.RunRef, error) {
	args := m.Called(ttl)
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

func (m *MockExecutor) Execute(ctx context.Context, task *model.Task, run *sqlcdb.Run) *executor.ExecuteResult {
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
