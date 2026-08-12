// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"sync"

	"github.com/runwisp/runwisp/internal/events"
)

const (
	// appEventRingSize bounds the replay history. Reconnects are milliseconds;
	// this covers minutes of lifecycle events, far more than any real gap.
	appEventRingSize = 1024
	// appSubBufSize is a live subscriber's buffer. On overflow the connection is
	// dropped (self-heals via replay on reconnect), so this only needs enough
	// headroom for a burst arriving while the pump is mid-send.
	appSubBufSize = 64
)

// seqEvent pairs a bus event with its monotonic positive stream id. The id is
// emitted as the SSE `id:` field so a reconnecting browser resumes from it.
type seqEvent struct {
	id int
	ev events.Event
}

// appSub is one live SSE connection's view of the log. dead is closed exactly
// once if the subscriber falls behind, so its pump returns and the client
// reconnects and replays from its last id instead of silently losing events.
type appSub struct {
	ch         chan seqEvent
	dead       chan struct{}
	overflowed bool // guards the single close(dead); touched only under appEventLog.mu
}

// appEventLog is the single bus consumer for the app-event SSE stream. It
// assigns each event a monotonic id, retains the last appEventRingSize in a
// ring, and fans out live events to per-connection subscribers. On reconnect a
// subscriber replays the ring tail past its last id, so a connection gap no
// longer loses events (the stream carried no ids and no replay before).
type appEventLog struct {
	mu   sync.Mutex
	seq  int
	ring []seqEvent
	subs map[*appSub]struct{}
}

func newAppEventLog() *appEventLog {
	return &appEventLog{subs: make(map[*appSub]struct{})}
}

// ingest is the bus handler. It runs on the publisher's goroutine (the bus
// invokes handlers synchronously), so it must never block: fan-out uses
// non-blocking sends and a full subscriber is disconnected rather than waited
// on.
//
// Fan-out MUST stay inside the same lock as the ring append: if they were
// split, a concurrent subscribe could both snapshot an event from the ring and
// register in time for its fan-out, delivering it twice at the replay/live
// boundary.
func (l *appEventLog) ingest(ev events.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seq++
	e := seqEvent{id: l.seq, ev: ev}

	l.ring = append(l.ring, e)
	if len(l.ring) > appEventRingSize {
		// ponytail: reslice ring; append reallocates periodically so memory
		// stays bounded to ~2x the window. Fixed-array head/count only if
		// profiling ever shows the churn matters.
		l.ring = l.ring[1:]
	}

	for s := range l.subs {
		if s.overflowed {
			continue
		}
		select {
		case s.ch <- e:
		default:
			s.overflowed = true
			close(s.dead)
		}
	}
}

// subscribe atomically snapshots the ring events past lastEventID AND registers
// the live subscriber, both under the lock, so the replay→live boundary is
// gap-free and dup-free: an event ordered before this call is in replay and its
// fan-out already ran before the sub existed (not live); an event ordered after
// is not in replay and its fan-out sees the sub (live). Every id > lastEventID
// still retained is delivered exactly once.
//
// lastEventID <= 0 means "no resume cursor" — a fresh client that seeds its
// history via REST and wants live events only, NOT the whole ring dumped on it.
// A reconnecting client passes the last id it saw (always >= 1, since the
// browser only learns an id after receiving an id'd event), replaying past it.
//
// unsub removes the subscriber under the lock and never closes sub.ch — after
// removal ingest can no longer reach it, so there is no send-on-closed race.
func (l *appEventLog) subscribe(lastEventID int) (replay []seqEvent, sub *appSub, unsub func()) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if lastEventID > 0 {
		for _, e := range l.ring {
			if e.id > lastEventID {
				replay = append(replay, e) // fresh slice; does not alias l.ring
			}
		}
	}

	sub = &appSub{ch: make(chan seqEvent, appSubBufSize), dead: make(chan struct{})}
	l.subs[sub] = struct{}{}

	unsub = func() {
		l.mu.Lock()
		delete(l.subs, sub)
		l.mu.Unlock()
	}
	return replay, sub, unsub
}
