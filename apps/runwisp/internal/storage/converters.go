// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

// runFromRow maps a storage row (sqlcdb.Run) to the public domain shape,
// dropping the row-internal DeletedAt column the rest of the daemon never
// sees. Pointer fields are shared with the source — the row is not retained
// after this call, so no aliasing problem in practice.
func runFromRow(s sqlcdb.Run) model.Run {
	return model.Run{
		ID:                  s.ID,
		ExternalExecutionID: s.ExternalExecutionID,
		TaskName:            s.TaskName,
		Status:              s.Status,
		EndReason:           s.EndReason,
		ExitCode:            s.ExitCode,
		StartAt:             s.StartAt,
		EndAt:               s.EndAt,
		TriggeredBy:         s.TriggeredBy,
		CreatedAt:           s.CreatedAt,
		RetryAttempt:        s.RetryAttempt,
		RetryOfRunID:        s.RetryOfRunID,
		InstanceIndex:       s.InstanceIndex,
	}
}

func runPtrFromRow(s sqlcdb.Run) *model.Run {
	r := runFromRow(s)
	return &r
}

// runToCreateParams maps a domain Run into sqlc CreateRun parameters.
func runToCreateParams(r *model.Run) sqlcdb.CreateRunParams {
	return sqlcdb.CreateRunParams{
		ID:                  r.ID,
		ExternalExecutionID: r.ExternalExecutionID,
		TaskName:            r.TaskName,
		Status:              r.Status,
		EndReason:           r.EndReason,
		ExitCode:            r.ExitCode,
		StartAt:             r.StartAt,
		EndAt:               r.EndAt,
		TriggeredBy:         r.TriggeredBy,
		CreatedAt:           r.CreatedAt,
		RetryAttempt:        r.RetryAttempt,
		RetryOfRunID:        r.RetryOfRunID,
		InstanceIndex:       r.InstanceIndex,
	}
}

// runToUpdateParams maps a domain Run into sqlc UpdateRun parameters.
func runToUpdateParams(r *model.Run) sqlcdb.UpdateRunParams {
	return sqlcdb.UpdateRunParams{
		ExternalExecutionID: r.ExternalExecutionID,
		TaskName:            r.TaskName,
		Status:              r.Status,
		EndReason:           r.EndReason,
		ExitCode:            r.ExitCode,
		StartAt:             r.StartAt,
		EndAt:               r.EndAt,
		TriggeredBy:         r.TriggeredBy,
		CreatedAt:           r.CreatedAt,
		RetryAttempt:        r.RetryAttempt,
		RetryOfRunID:        r.RetryOfRunID,
		InstanceIndex:       r.InstanceIndex,
		ID:                  r.ID,
	}
}

// notificationFromSqlcdb maps a sqlcdb.Notification row to the public
// Notification shape, decoding the occurrences JSON column.
func notificationFromSqlcdb(s sqlcdb.Notification) (*Notification, error) {
	occ, err := decodeOccurrences(s.OccurrencesJson)
	if err != nil {
		return nil, err
	}
	return &Notification{
		ID:             s.ID,
		Fingerprint:    s.Fingerprint,
		Kind:           s.Kind,
		Severity:       s.Severity,
		TaskName:       s.TaskName,
		RunID:          s.RunID,
		Title:          s.Title,
		Body:           s.Body,
		Count:          s.Count,
		Occurrences:    occ,
		CreatedAt:      s.CreatedAt,
		LastOccurredAt: s.LastOccurredAt,
		ReadAt:         s.ReadAt,
	}, nil
}

// pendingLogUploadFromRow maps a sqlcdb.PendingLogUpload row to the public
// model.PendingLogUpload shape. The two structs differ only in field naming
// (sqlc emits UploadUrl from the snake-cased column; the domain type uses
// the Go-idiomatic UploadURL).
func pendingLogUploadFromRow(s sqlcdb.PendingLogUpload) model.PendingLogUpload {
	return model.PendingLogUpload{
		ExternalExecutionID: s.ExternalExecutionID,
		UploadURL:           s.UploadUrl,
		LogPath:             s.LogPath,
		InsertedAt:          s.InsertedAt,
	}
}
