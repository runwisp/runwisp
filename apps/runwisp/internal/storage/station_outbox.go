// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runwisp/runwisp/apps/runwisp/internal/storage/sqlcdb"
)

// OutboxUpdate is one stored execution update waiting for the station's ack.
// Payload is the update's JSON without its seq, which is the row key.
type OutboxUpdate struct {
	Seq         int64
	ExecutionID string
	Finished    bool
	Payload     []byte
}

// OutboxExecution is one execution with updates still in the outbox; Finished
// is set when one of them reports it finished.
type OutboxExecution struct {
	ExecutionID string
	Finished    bool
}

// StationOutboxRepository stores execution updates until the station acks
// them. Seq numbers only ever grow, across deletes and restarts.
type StationOutboxRepository interface {
	// AppendOutboxUpdate stores an update and returns the seq it was assigned.
	AppendOutboxUpdate(ctx context.Context, executionID string, finished bool, payload []byte) (int64, error)
	// ListOutboxUpdates returns up to limit updates with seq > after, in order.
	ListOutboxUpdates(ctx context.Context, after int64, limit int) ([]OutboxUpdate, error)
	// AckOutboxUpdates deletes every update with seq <= seq.
	AckOutboxUpdates(ctx context.Context, seq int64) error
	ListOutboxExecutions(ctx context.Context) ([]OutboxExecution, error)
	// OutboxLastSeq is the highest seq ever assigned, 0 when none was.
	OutboxLastSeq(ctx context.Context) (int64, error)
}

func (db *SQLiteDatabase) AppendOutboxUpdate(ctx context.Context, executionID string, finished bool, payload []byte) (int64, error) {
	return db.q.InsertOutboxUpdate(ctx, sqlcdb.InsertOutboxUpdateParams{
		ExecutionID: executionID,
		Finished:    boolToInt64(finished),
		Payload:     string(payload),
	})
}

func (db *SQLiteDatabase) ListOutboxUpdates(ctx context.Context, after int64, limit int) ([]OutboxUpdate, error) {
	rows, err := db.q.ListOutboxUpdates(ctx, sqlcdb.ListOutboxUpdatesParams{Seq: after, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]OutboxUpdate, len(rows))
	for i, r := range rows {
		out[i] = OutboxUpdate{Seq: r.Seq, ExecutionID: r.ExecutionID, Finished: r.Finished != 0, Payload: []byte(r.Payload)}
	}
	return out, nil
}

func (db *SQLiteDatabase) AckOutboxUpdates(ctx context.Context, seq int64) error {
	return db.q.DeleteOutboxUpdatesThrough(ctx, seq)
}

func (db *SQLiteDatabase) ListOutboxExecutions(ctx context.Context) ([]OutboxExecution, error) {
	rows, err := db.q.ListOutboxExecutions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]OutboxExecution, len(rows))
	for i, r := range rows {
		out[i] = OutboxExecution{ExecutionID: r.ExecutionID, Finished: r.Finished != 0}
	}
	return out, nil
}

// OutboxLastSeq reads sqlite_sequence, which AUTOINCREMENT keeps at the highest
// seq ever used even once every row is deleted. Raw SQL because sqlc doesn't
// model that internal table.
func (db *SQLiteDatabase) OutboxLastSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := db.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = 'station_outbox'`).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
}
