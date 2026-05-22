// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

// cloudTaskRunner adapts the concrete runtime task manager to cloud.TaskRunner.
// Specifically it translates the cloud's "trigger a cloud-tagged run for this
// execution id" call into the runtime's richer TriggerRunWithOptions API.
type cloudTaskRunner struct {
	inner runtime.TaskRunner
}

func (a *cloudTaskRunner) GetTask(name string) (*model.Task, bool) {
	return a.inner.GetTask(name)
}

func (a *cloudTaskRunner) UpsertTask(task *model.Task) {
	a.inner.UpsertTask(task)
}

func (a *cloudTaskRunner) TriggerCloudRun(taskName, externalExecutionID string) (*sqlcdb.Run, error) {
	return a.inner.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy:         sqlcdb.TriggeredByCloud,
		ExternalExecutionID: externalExecutionID,
	})
}

func (a *cloudTaskRunner) TerminateRunByExternalExecutionID(externalExecutionID string) error {
	return a.inner.TerminateRunByExternalExecutionID(externalExecutionID)
}
