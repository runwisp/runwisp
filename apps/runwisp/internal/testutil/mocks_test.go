// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/mock"
)

func TestMockRunRepository(t *testing.T) {
	ctx := context.Background()
	m := &MockRunRepository{}

	run := &model.Run{ID: "r1"}
	task := &model.Task{Name: "t1"}
	wantErr := errors.New("boom")

	m.On("CreateRun", ctx, run).Return(nil)
	m.On("UpdateRun", ctx, run).Return(nil)
	m.On("GetRun", ctx, "r1").Return(run, nil)
	m.On("GetRun", ctx, "missing").Return((*model.Run)(nil), wantErr)
	m.On("GetRunByExternalExecutionID", ctx, "x").Return(run, nil)
	m.On("GetRunByExternalExecutionID", ctx, "missing").Return((*model.Run)(nil), wantErr)
	m.On("CountRuns", ctx, "t1").Return(int64(3), nil)
	m.On("CountRunsFiltered", ctx, model.RunFilter{TaskName: "t1", Search: "q"}).Return(int64(2), nil)
	queryRunsArg := storage.RunQuery{
		Filter: model.RunFilter{TaskName: "t1"},
		Limit:  10,
	}
	m.On("QueryRuns", ctx, queryRunsArg).Return([]model.Run{*run}, nil)
	m.On("DeleteRun", ctx, "r1").Return(nil)
	m.On("DeleteOldRuns", ctx, task).Return([]model.Run{*run}, nil)
	m.On("MarkCrashedRuns", ctx).Return(int64(1), nil)
	m.On("GetPendingRuns", ctx).Return([]model.Run{*run}, nil)
	m.On("GetLastRunByTask", ctx, "t1").Return(run, nil)
	m.On("GetLastRunByTask", ctx, "missing").Return((*model.Run)(nil), wantErr)
	summary := &model.RunSummary{}
	m.On("GetRunSummary", ctx).Return(summary, nil)
	now := time.Unix(0, 0)
	m.On("EnsureTaskRegistered", ctx, "t1", now).Return(nil)
	reg := &model.TaskRegistration{}
	m.On("GetTaskRegistration", ctx, "t1").Return(reg, nil)
	m.On("GetTaskRegistration", ctx, "missing").Return((*model.TaskRegistration)(nil), wantErr)
	sel := model.RunSelector{MatchAll: true}
	refs := []storage.RunRef{{ID: "r1"}}
	m.On("SoftDeleteRuns", ctx, sel, now).Return(refs, nil)
	m.On("RestoreRuns", ctx, sel).Return([]model.Run{*run}, nil)
	m.On("ResolveSelectorIDs", ctx, sel, "").Return(refs, nil)
	m.On("PurgeExpiredSoftDeletes", ctx, time.Hour).Return(refs, nil)
	m.On("Close").Return(nil)

	if err := m.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := m.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}
	if got, err := m.GetRun(ctx, "r1"); err != nil || got != run {
		t.Fatalf("GetRun hit: %v %v", got, err)
	}
	if got, err := m.GetRun(ctx, "missing"); got != nil || !errors.Is(err, wantErr) {
		t.Fatalf("GetRun miss: %v %v", got, err)
	}
	if got, err := m.GetRunByExternalExecutionID(ctx, "x"); err != nil || got != run {
		t.Fatalf("GetRunByExternalExecutionID hit: %v %v", got, err)
	}
	if got, err := m.GetRunByExternalExecutionID(ctx, "missing"); got != nil || !errors.Is(err, wantErr) {
		t.Fatalf("GetRunByExternalExecutionID miss: %v %v", got, err)
	}
	if n, err := m.CountRuns(ctx, "t1"); err != nil || n != 3 {
		t.Fatalf("CountRuns: %d %v", n, err)
	}
	if n, err := m.CountRunsFiltered(ctx, model.RunFilter{TaskName: "t1", Search: "q"}); err != nil || n != 2 {
		t.Fatalf("CountRunsFiltered: %d %v", n, err)
	}
	if got, err := m.QueryRuns(ctx, queryRunsArg); err != nil || len(got) != 1 {
		t.Fatalf("QueryRuns: %v %v", got, err)
	}
	if err := m.DeleteRun(ctx, "r1"); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if got, err := m.DeleteOldRuns(ctx, task); err != nil || len(got) != 1 {
		t.Fatalf("DeleteOldRuns: %v %v", got, err)
	}
	if n, err := m.MarkCrashedRuns(ctx); err != nil || n != 1 {
		t.Fatalf("MarkCrashedRuns: %d %v", n, err)
	}
	if got, err := m.GetPendingRuns(ctx); err != nil || len(got) != 1 {
		t.Fatalf("GetPendingRuns: %v %v", got, err)
	}
	if got, err := m.GetLastRunByTask(ctx, "t1"); err != nil || got != run {
		t.Fatalf("GetLastRunByTask hit: %v %v", got, err)
	}
	if got, err := m.GetLastRunByTask(ctx, "missing"); got != nil || !errors.Is(err, wantErr) {
		t.Fatalf("GetLastRunByTask miss: %v %v", got, err)
	}
	if got, err := m.GetRunSummary(ctx); err != nil || got != summary {
		t.Fatalf("GetRunSummary: %v %v", got, err)
	}
	if err := m.EnsureTaskRegistered(ctx, "t1", now); err != nil {
		t.Fatalf("EnsureTaskRegistered: %v", err)
	}
	if got, err := m.GetTaskRegistration(ctx, "t1"); err != nil || got != reg {
		t.Fatalf("GetTaskRegistration hit: %v %v", got, err)
	}
	if got, err := m.GetTaskRegistration(ctx, "missing"); got != nil || !errors.Is(err, wantErr) {
		t.Fatalf("GetTaskRegistration miss: %v %v", got, err)
	}
	if got, err := m.SoftDeleteRuns(ctx, sel, now); err != nil || len(got) != 1 {
		t.Fatalf("SoftDeleteRuns: %v %v", got, err)
	}
	if got, err := m.RestoreRuns(ctx, sel); err != nil || len(got) != 1 {
		t.Fatalf("RestoreRuns: %v %v", got, err)
	}
	if got, err := m.ResolveSelectorIDs(ctx, sel, ""); err != nil || len(got) != 1 {
		t.Fatalf("ResolveSelectorIDs: %v %v", got, err)
	}
	if got, err := m.PurgeExpiredSoftDeletes(ctx, time.Hour); err != nil || len(got) != 1 {
		t.Fatalf("PurgeExpiredSoftDeletes: %v %v", got, err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mNil := &MockRunRepository{}
	mNil.On("GetRun", ctx, "x").Return(nil, errors.New("not found"))
	mNil.On("GetRunByExternalExecutionID", ctx, "x").Return(nil, errors.New("nf"))
	mNil.On("GetLastRunByTask", ctx, "x").Return(nil, errors.New("nf"))
	mNil.On("GetRunSummary", ctx).Return(nil, errors.New("nf"))
	mNil.On("GetTaskRegistration", ctx, "x").Return(nil, errors.New("nf"))
	mNil.On("SoftDeleteRuns", ctx, sel, now).Return(nil, errors.New("nf"))
	mNil.On("RestoreRuns", ctx, sel).Return(nil, errors.New("nf"))
	mNil.On("ResolveSelectorIDs", ctx, sel, "").Return(nil, errors.New("nf"))
	mNil.On("PurgeExpiredSoftDeletes", ctx, time.Hour).Return(nil, errors.New("nf"))
	if _, err := mNil.GetRun(ctx, "x"); err == nil {
		t.Fatal("expect err")
	}
	if _, err := mNil.GetRunByExternalExecutionID(ctx, "x"); err == nil {
		t.Fatal("expect err")
	}
	if _, err := mNil.GetLastRunByTask(ctx, "x"); err == nil {
		t.Fatal("expect err")
	}
	if _, err := mNil.GetRunSummary(ctx); err == nil {
		t.Fatal("expect err")
	}
	if _, err := mNil.GetTaskRegistration(ctx, "x"); err == nil {
		t.Fatal("expect err")
	}
	if _, err := mNil.SoftDeleteRuns(ctx, sel, now); err == nil {
		t.Fatal("expect err")
	}
	if _, err := mNil.RestoreRuns(ctx, sel); err == nil {
		t.Fatal("expect err")
	}
	if _, err := mNil.ResolveSelectorIDs(ctx, sel, ""); err == nil {
		t.Fatal("expect err")
	}
	if _, err := mNil.PurgeExpiredSoftDeletes(ctx, time.Hour); err == nil {
		t.Fatal("expect err")
	}
}

func TestMockExecutor(t *testing.T) {
	m := &MockExecutor{}
	task := &model.Task{Name: "t"}
	run := &model.Run{ID: "r"}

	want := &executor.ExecuteResult{ExitCode: 0}
	m.On("Execute", mock.Anything, task, run).Return(want)
	got := m.Execute(context.Background(), task, run)
	if got != want {
		t.Fatalf("Execute: %v", got)
	}

	if a := m.Availability(); a != (executor.Availability{}) {
		t.Fatalf("Availability: %+v", a)
	}

	mCancel := &MockExecutor{}
	mCancel.On("Execute", mock.Anything, task, run).Return(want, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := mCancel.Execute(ctx, task, run)
	if !r.Stopped {
		t.Fatalf("expected Stopped on canceled ctx, got %+v", r)
	}

	mDelay := &MockExecutor{}
	mDelay.On("Execute", mock.Anything, task, run).Return(want, time.Duration(0))
	r2 := mDelay.Execute(context.Background(), task, run)
	if r2 != want {
		t.Fatalf("Execute with 0 delay: %v", r2)
	}

	mTO := &MockExecutor{}
	mTO.On("Execute", mock.Anything, task, run).Return(want, 10*time.Millisecond)
	ctxTO, cancelTO := context.WithDeadline(context.Background(), time.Now().Add(time.Microsecond))
	defer cancelTO()
	rTO := mTO.Execute(ctxTO, task, run)
	if !rTO.TimedOut {
		t.Fatalf("expected TimedOut, got %+v", rTO)
	}
}
