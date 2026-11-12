// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// outboxBatch is how many stored updates one read pulls for sending.
const outboxBatch = 100

// outbox is the durable queue of execution updates: each one is stored (and
// gets its seq) before it is sent, and stays until the station acks it. A
// session streams it in seq order from the start after every welcome, so an
// update survives a dropped socket, a station restart and a daemon restart.
type outbox struct {
	repo storage.StationOutboxRepository
	// wake nudges the attached session's sender after an append.
	wake chan struct{}
}

func newOutbox(repo storage.StationOutboxRepository) *outbox {
	return &outbox{repo: repo, wake: make(chan struct{}, 1)}
}

// append stores update and wakes the sender. A failed write is logged: the
// execution then shows up in no inventory, and the station marks it lost.
func (o *outbox) append(update *protocol.ExecutionUpdate) {
	if update == nil {
		return
	}
	update.Seq = 0 // assigned by the store
	payload, err := json.Marshal(update)
	if err == nil {
		_, err = o.repo.AppendOutboxUpdate(context.Background(), update.ExecutionID, update.Status == protocol.ExecutionStatusFinished, payload)
	}
	if err != nil {
		slog.Error("failed to store execution update for the station", "executionId", update.ExecutionID, "status", update.Status, "err", err)
		return
	}
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// read returns up to outboxBatch updates after seq, each carrying its seq,
// and the seq to read after next time.
func (o *outbox) read(ctx context.Context, after int64) ([]protocol.ExecutionUpdate, int64, error) {
	rows, err := o.repo.ListOutboxUpdates(ctx, after, outboxBatch)
	if err != nil {
		return nil, after, err
	}
	out := make([]protocol.ExecutionUpdate, 0, len(rows))
	for _, row := range rows {
		after = row.Seq
		var u protocol.ExecutionUpdate
		if err := json.Unmarshal(row.Payload, &u); err != nil {
			slog.Error("dropping unreadable stored execution update", "seq", row.Seq, "err", err)
			continue
		}
		u.Seq = row.Seq
		out = append(out, u)
	}
	return out, after, nil
}

// ack drops every update up to seq: the station applied them durably.
func (o *outbox) ack(seq int64) {
	if err := o.repo.AckOutboxUpdates(context.Background(), seq); err != nil {
		slog.Warn("failed to drop acked execution updates; they will be resent", "seq", seq, "err", err)
	}
}

func (o *outbox) executions(ctx context.Context) []storage.OutboxExecution {
	execs, err := o.repo.ListOutboxExecutions(ctx)
	if err != nil {
		slog.Warn("failed to list unacked execution updates", "err", err)
	}
	return execs
}

func (o *outbox) lastSeq(ctx context.Context) int64 {
	seq, err := o.repo.OutboxLastSeq(ctx)
	if err != nil {
		slog.Warn("failed to read the last execution update seq", "err", err)
	}
	return seq
}
