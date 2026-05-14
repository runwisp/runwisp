// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"

	"github.com/runwisp/runwisp/internal/cloud"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
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

func (a *cloudTaskRunner) TriggerCloudRun(taskName, externalExecutionID string) (*model.Run, error) {
	return a.inner.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy:         model.TriggeredByCloud,
		ExternalExecutionID: externalExecutionID,
	})
}

func (a *cloudTaskRunner) TerminateRunByExternalExecutionID(externalExecutionID string) error {
	return a.inner.TerminateRunByExternalExecutionID(externalExecutionID)
}

// cloudRunRepo adapts the storage run repository to cloud.RunRepository. The
// only translation needed is mapping storage.ErrNotFound to cloud.ErrNotFound
// so callers inside the cloud package can use errors.Is without importing
// internal/storage.
type cloudRunRepo struct {
	inner storage.RunRepository
}

func (a *cloudRunRepo) GetRunByExternalExecutionID(externalExecutionID string) (*model.Run, error) {
	run, err := a.inner.GetRunByExternalExecutionID(externalExecutionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, cloud.ErrNotFound
		}
		return nil, err
	}
	return run, nil
}

// cloudPendingUploadRepo adapts the storage pending-upload repository to the
// cloud.PendingLogUploadRepository interface, translating the
// storage.PendingLogUpload record shape to the cloud-local mirror.
type cloudPendingUploadRepo struct {
	inner storage.PendingLogUploadRepository
}

func (a *cloudPendingUploadRepo) UpsertPendingLogUpload(rec cloud.PendingLogUpload) error {
	return a.inner.UpsertPendingLogUpload(storage.PendingLogUpload{
		ExternalExecutionID: rec.ExternalExecutionID,
		UploadURL:           rec.UploadURL,
		LogPath:             rec.LogPath,
		InsertedAt:          rec.InsertedAt,
	})
}

func (a *cloudPendingUploadRepo) DeletePendingLogUpload(externalExecutionID string) error {
	return a.inner.DeletePendingLogUpload(externalExecutionID)
}

func (a *cloudPendingUploadRepo) ListPendingLogUploads() ([]cloud.PendingLogUpload, error) {
	rows, err := a.inner.ListPendingLogUploads()
	if err != nil {
		return nil, err
	}
	out := make([]cloud.PendingLogUpload, len(rows))
	for i, r := range rows {
		out[i] = cloud.PendingLogUpload{
			ExternalExecutionID: r.ExternalExecutionID,
			UploadURL:           r.UploadURL,
			LogPath:             r.LogPath,
			InsertedAt:          r.InsertedAt,
		}
	}
	return out, nil
}
