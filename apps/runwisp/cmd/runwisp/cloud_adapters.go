// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
)

// cloudRuntime is exactly the runtime surface the cloud adapter drives: the
// observe/trigger TaskRunner plus RemoveTask (service:remove tears down a
// cloud-declared service). A concrete runtime.TaskManager satisfies it; naming
// the seam here keeps the adapter — and its tests — from depending on the full
// TaskManager lifecycle interface.
type cloudRuntime interface {
	runtime.TaskRunner
	RemoveTask(taskName string)
}

// cloudTaskRunner adapts the concrete runtime task manager to cloud.TaskRunner.
// Specifically it translates the cloud's "trigger a cloud-tagged run for this
// execution id" call into the runtime's richer TriggerRunWithOptions API.
type cloudTaskRunner struct {
	inner cloudRuntime
}

func (a *cloudTaskRunner) GetTask(name string) (*model.Task, bool) {
	return a.inner.GetTask(name)
}

func (a *cloudTaskRunner) ListServiceTasks() []*model.Task {
	return a.inner.ListServiceTasks()
}

func (a *cloudTaskRunner) UpsertTask(task *model.Task) {
	a.inner.UpsertTask(task)
}

func (a *cloudTaskRunner) RemoveTask(taskName string) {
	a.inner.RemoveTask(taskName)
}

func (a *cloudTaskRunner) TriggerCloudRun(taskName, externalExecutionID string, params map[string]string) (*model.Run, error) {
	return a.inner.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy:         model.TriggeredByCloud,
		ExternalExecutionID: externalExecutionID,
		// The cloud protocol carries plain string values (no explicit-omit state),
		// so every supplied key is a present value; absent keys use the default.
		Params: model.PointerValues(params),
	})
}

func (a *cloudTaskRunner) TerminateRunByExternalExecutionID(externalExecutionID string) error {
	return a.inner.TerminateRunByExternalExecutionID(externalExecutionID)
}

func (a *cloudTaskRunner) StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error {
	return a.inner.StartServiceInstances(taskName, triggeredBy)
}

func (a *cloudTaskRunner) StopService(taskName string) error {
	return a.inner.StopService(taskName)
}

func (a *cloudTaskRunner) RestartServiceInstances(taskName string) error {
	return a.inner.RestartServiceInstances(taskName)
}

func (a *cloudTaskRunner) ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool) {
	return a.inner.ServiceSnapshot(taskName)
}
