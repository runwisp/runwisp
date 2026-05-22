// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

// runFromSqlcdb maps a sqlcdb.Run row to the public sqlcdb.Run shape. LogPath
// is intentionally left empty — it is a runtime attachment set by the executor
// and is not persisted in the runs table.
func runFromSqlcdb(s sqlcdb.Run) sqlcdb.Run {
	var endReason *sqlcdb.EndReason
	if s.EndReason != nil {
		er := sqlcdb.EndReason(*s.EndReason)
		endReason = &er
	}
	return sqlcdb.Run{
		ID:                  s.ID,
		ExternalExecutionID: s.ExternalExecutionID,
		TaskName:            s.TaskName,
		Status:              sqlcdb.RunPhase(s.Status),
		EndReason:           endReason,
		ExitCode:            s.ExitCode,
		StartAt:             s.StartAt,
		EndAt:               s.EndAt,
		TriggeredBy:         sqlcdb.TriggeredBy(s.TriggeredBy),
		CreatedAt:           s.CreatedAt,
		RetryAttempt:        s.RetryAttempt,
		RetryOfRunID:        s.RetryOfRunID,
		InstanceIndex:       s.InstanceIndex,
	}
}

func runPtrFromSqlcdb(s sqlcdb.Run) *sqlcdb.Run {
	r := runFromSqlcdb(s)
	return &r
}

func endReasonToSqlcdb(r *sqlcdb.EndReason) *sqlcdb.EndReason {
	if r == nil {
		return nil
	}
	er := sqlcdb.EndReason(*r)
	return &er
}

// runToCreateParams maps a sqlcdb.Run into sqlc CreateRun parameters.
func runToCreateParams(r *sqlcdb.Run) sqlcdb.CreateRunParams {
	return sqlcdb.CreateRunParams{
		ID:                  r.ID,
		ExternalExecutionID: r.ExternalExecutionID,
		TaskName:            r.TaskName,
		Status:              sqlcdb.RunPhase(r.Status),
		EndReason:           endReasonToSqlcdb(r.EndReason),
		ExitCode:            r.ExitCode,
		StartAt:             r.StartAt,
		EndAt:               r.EndAt,
		TriggeredBy:         sqlcdb.TriggeredBy(r.TriggeredBy),
		CreatedAt:           r.CreatedAt,
		RetryAttempt:        r.RetryAttempt,
		RetryOfRunID:        r.RetryOfRunID,
		InstanceIndex:       r.InstanceIndex,
	}
}

// runToUpdateParams maps a sqlcdb.Run into sqlc UpdateRun parameters.
func runToUpdateParams(r *sqlcdb.Run) sqlcdb.UpdateRunParams {
	return sqlcdb.UpdateRunParams{
		ExternalExecutionID: r.ExternalExecutionID,
		TaskName:            r.TaskName,
		Status:              sqlcdb.RunPhase(r.Status),
		EndReason:           endReasonToSqlcdb(r.EndReason),
		ExitCode:            r.ExitCode,
		StartAt:             r.StartAt,
		EndAt:               r.EndAt,
		TriggeredBy:         sqlcdb.TriggeredBy(r.TriggeredBy),
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

// pendingLogUploadFromSqlcdb maps a sqlcdb.PendingLogUpload row to the public
// sqlcdb.PendingLogUpload shape. The on-disk inserted_at column is INTEGER
// epoch seconds — sqlcdb.PendingLogUpload preserves the same int64 shape.
func pendingLogUploadFromSqlcdb(s sqlcdb.PendingLogUpload) sqlcdb.PendingLogUpload {
	return sqlcdb.PendingLogUpload{
		ExternalExecutionID: s.ExternalExecutionID,
		UploadUrl:           s.UploadUrl,
		LogPath:             s.LogPath,
		InsertedAt:          s.InsertedAt,
	}
}
