// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTaskRunner is a hand-rolled implementation of runtime.TaskRunner used to
// verify that cloudTaskRunner delegates each method correctly.
type stubTaskRunner struct {
	gotTrigger         runtime.TriggerRunOptions
	gotTriggerTask     string
	triggerRun         *model.Run
	triggerErr         error
	getTaskName        string
	getTaskOut         *model.Task
	getTaskOK          bool
	upsertedTask       *model.Task
	terminatedExternal string
	terminatedErr      error
}

func (s *stubTaskRunner) TriggerRun(string, model.TriggeredBy) (*model.Run, error) {
	panic("not used")
}

func (s *stubTaskRunner) TriggerRunWithOptions(taskName string, opts runtime.TriggerRunOptions) (*model.Run, error) {
	s.gotTriggerTask = taskName
	s.gotTrigger = opts
	return s.triggerRun, s.triggerErr
}

func (s *stubTaskRunner) GetTask(name string) (*model.Task, bool) {
	s.getTaskName = name
	return s.getTaskOut, s.getTaskOK
}

func (s *stubTaskRunner) UpsertTask(t *model.Task) {
	s.upsertedTask = t
}

func (s *stubTaskRunner) TerminateRun(string) error { panic("not used") }

func (s *stubTaskRunner) TerminateRunByExternalExecutionID(id string) error {
	s.terminatedExternal = id
	return s.terminatedErr
}

func (s *stubTaskRunner) RestartServiceInstances(string) error { panic("not used") }
func (s *stubTaskRunner) StopService(string) error             { panic("not used") }
func (s *stubTaskRunner) RecordSkippedFiring(string, model.EndReason, model.TriggeredBy) error {
	panic("not used")
}
func (s *stubTaskRunner) GetActiveRunCount(string) int { panic("not used") }

func TestCloudTaskRunner_GetTask_DelegatesAndPropagates(t *testing.T) {
	inner := &stubTaskRunner{
		getTaskOut: &model.Task{Name: "backup"},
		getTaskOK:  true,
	}
	a := &cloudTaskRunner{inner: inner}

	got, ok := a.GetTask("backup")
	assert.True(t, ok)
	require.NotNil(t, got)
	assert.Equal(t, "backup", got.Name)
	assert.Equal(t, "backup", inner.getTaskName)
}

func TestCloudTaskRunner_UpsertTask_DelegatesTask(t *testing.T) {
	inner := &stubTaskRunner{}
	a := &cloudTaskRunner{inner: inner}

	task := &model.Task{Name: "alpha"}
	a.UpsertTask(task)
	assert.Same(t, task, inner.upsertedTask)
}

func TestCloudTaskRunner_TriggerCloudRun_TagsTriggeredByCloud(t *testing.T) {
	want := &model.Run{ID: "r1", TaskName: "alpha", CreatedAt: time.Now()}
	inner := &stubTaskRunner{triggerRun: want}
	a := &cloudTaskRunner{inner: inner}

	got, err := a.TriggerCloudRun("alpha", "exec-123")
	require.NoError(t, err)
	assert.Same(t, want, got)
	assert.Equal(t, "alpha", inner.gotTriggerTask)
	assert.Equal(t, model.TriggeredByCloud, inner.gotTrigger.TriggeredBy)
	assert.Equal(t, "exec-123", inner.gotTrigger.ExternalExecutionID)
}

func TestCloudTaskRunner_TriggerCloudRun_PropagatesError(t *testing.T) {
	boom := errors.New("trigger failed")
	inner := &stubTaskRunner{triggerErr: boom}
	a := &cloudTaskRunner{inner: inner}

	_, err := a.TriggerCloudRun("x", "e")
	assert.ErrorIs(t, err, boom)
}

func TestCloudTaskRunner_TerminateRunByExternalExecutionID_Delegates(t *testing.T) {
	inner := &stubTaskRunner{}
	a := &cloudTaskRunner{inner: inner}

	require.NoError(t, a.TerminateRunByExternalExecutionID("ext-42"))
	assert.Equal(t, "ext-42", inner.terminatedExternal)
}

func TestCloudTaskRunner_TerminateRunByExternalExecutionID_PropagatesError(t *testing.T) {
	boom := errors.New("nope")
	inner := &stubTaskRunner{terminatedErr: boom}
	a := &cloudTaskRunner{inner: inner}

	err := a.TerminateRunByExternalExecutionID("ext")
	assert.ErrorIs(t, err, boom)
}
