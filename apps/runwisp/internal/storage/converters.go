// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

// runFromRow maps a storage row to model.Run, dropping DeletedAt and decoding
// params_json (corrupt JSON degrades to nil params, see decodeParams).
func runFromRow(s sqlcdb.Run) model.Run {
	return model.Run{
		ID:            s.ID,
		ExecutionID:   s.ExecutionID,
		TaskName:      s.TaskName,
		Status:        s.Status,
		EndReason:     s.EndReason,
		ExitCode:      s.ExitCode,
		StartedAt:     s.StartedAt,
		EndedAt:       s.EndedAt,
		TriggeredBy:   s.TriggeredBy,
		CreatedAt:     s.CreatedAt,
		RetryAttempt:  s.RetryAttempt,
		RetryOfRunID:  s.RetryOfRunID,
		InstanceIndex: s.InstanceIndex,
		IsFailure:     s.IsFailure != 0,
		Params:        decodeParams(s.ParamsJson, s.ID),

		PeakMemoryBytes: s.PeakMemoryBytes,
		CPUTimeMs:       s.CpuTimeMs,
	}
}

func runPtrFromRow(s sqlcdb.Run) *model.Run {
	r := runFromRow(s)
	return &r
}

// runsFromRows maps a slice of storage rows to domain runs.
func runsFromRows(rows []sqlcdb.Run) []model.Run {
	out := make([]model.Run, 0, len(rows))
	for _, r := range rows {
		out = append(out, runFromRow(r))
	}
	return out
}

// collectRunsByID maps storage rows into dst keyed by run ID. Used by retention,
// which unions runs matched by age and by count into one dedup map.
func collectRunsByID(dst map[string]model.Run, rows []sqlcdb.Run) {
	for _, r := range rows {
		dst[r.ID] = runFromRow(r)
	}
}

// runToCreateParams maps a domain Run into sqlc CreateRun parameters.
func runToCreateParams(r *model.Run) sqlcdb.CreateRunParams {
	return sqlcdb.CreateRunParams{
		ID:            r.ID,
		ExecutionID:   r.ExecutionID,
		TaskName:      r.TaskName,
		Status:        r.Status,
		EndReason:     r.EndReason,
		ExitCode:      r.ExitCode,
		StartedAt:     utcPtr(r.StartedAt),
		EndedAt:       utcPtr(r.EndedAt),
		TriggeredBy:   r.TriggeredBy,
		CreatedAt:     r.CreatedAt.UTC(),
		RetryAttempt:  r.RetryAttempt,
		RetryOfRunID:  r.RetryOfRunID,
		InstanceIndex: r.InstanceIndex,
		ParamsJson:    encodeParams(r.Params),
		IsFailure:     boolToInt64(r.IsFailure),

		PeakMemoryBytes: r.PeakMemoryBytes,
		CpuTimeMs:       r.CPUTimeMs,
	}
}

// runToUpdateParams maps a domain Run into sqlc UpdateRun parameters.
func runToUpdateParams(r *model.Run) sqlcdb.UpdateRunParams {
	return sqlcdb.UpdateRunParams{
		ExecutionID:   r.ExecutionID,
		TaskName:      r.TaskName,
		Status:        r.Status,
		EndReason:     r.EndReason,
		ExitCode:      r.ExitCode,
		StartedAt:     utcPtr(r.StartedAt),
		EndedAt:       utcPtr(r.EndedAt),
		TriggeredBy:   r.TriggeredBy,
		CreatedAt:     r.CreatedAt.UTC(),
		RetryAttempt:  r.RetryAttempt,
		RetryOfRunID:  r.RetryOfRunID,
		InstanceIndex: r.InstanceIndex,
		ParamsJson:    encodeParams(r.Params),
		IsFailure:     boolToInt64(r.IsFailure),
		ID:            r.ID,

		PeakMemoryBytes: r.PeakMemoryBytes,
		CpuTimeMs:       r.CPUTimeMs,
	}
}

// utcPtr normalizes a nullable time to UTC before it reaches SQLite, which
// stores the offset verbatim, so TEXT comparisons (created_at >= ?) only order
// correctly when every row shares one offset.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// boolToInt64 encodes the is_failure classification bit for its INTEGER column
// (SQLite has no native boolean); runFromRow decodes it with `!= 0`.
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// encodeParams serialises the resolved per-run parameter map for the
// params_json column. An empty map stores NULL so zero-param runs round-trip
// to nil. Marshalling a map[string]string cannot fail, so no error is surfaced.
func encodeParams(params map[string]string) *string {
	if len(params) == 0 {
		return nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// decodeParams parses the params_json column back into a map. NULL/empty yields
// a nil map (the zero-param case). A malformed value never fails the row: the
// run still exists, only its params are unreadable, so it degrades to nil with
// a warning rather than wedging an entire batch (e.g. GetPendingRuns on boot).
func decodeParams(raw *string, runID string) map[string]string {
	if raw == nil || *raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*raw), &m); err != nil {
		slog.Warn("ignoring corrupt params_json column", "run", runID, "err", err)
		return nil
	}
	return m
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
		ExecutionID: s.ExecutionID,
		UploadURL:   s.UploadUrl,
		LogPath:     s.LogPath,
		InsertedAt:  s.InsertedAtUnix,
	}
}
