// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"sync"
	"testing"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/stretchr/testify/require"
)

// ev builds a minimal bus event; ingest only cares about identity/order, not
// the payload, so the type is arbitrary.
func ev() events.Event {
	return events.Event{Type: events.EventRunStarted, Data: events.RunEvent{}}
}

func ids(evs []seqEvent) []int {
	out := make([]int, len(evs))
	for i, e := range evs {
		out[i] = e.id
	}
	return out
}

// A fresh client (no resume cursor) gets NO historical replay — it seeds via
// REST and only wants live events. Otherwise it would get the whole ring dumped
// on it (stale runs bloat the list, stale samples spam the chart).
func TestAppEventLogFreshClientGetsNoReplay(t *testing.T) {
	l := newAppEventLog()
	l.ingest(ev()) // id 1
	l.ingest(ev()) // id 2

	replay, sub, unsub := l.subscribe(0)
	defer unsub()
	require.Empty(t, replay, "fresh client must not receive backfill")

	l.ingest(ev()) // id 3 -> live
	select {
	case e := <-sub.ch:
		require.Equal(t, 3, e.id)
	default:
		t.Fatal("expected live event 3")
	}
}

// (a) An event published while no subscriber is connected is delivered on
// reconnect with the client's last id.
func TestAppEventLogReplaysMissedDuringDisconnect(t *testing.T) {
	l := newAppEventLog()
	// Client connected fresh and saw 1,2 live, then disconnected.
	_, _, unsub := l.subscribe(0)
	l.ingest(ev()) // id 1
	l.ingest(ev()) // id 2
	unsub()        // client disconnects

	l.ingest(ev()) // id 3, published while disconnected

	replay, _, unsub2 := l.subscribe(2)
	defer unsub2()
	require.Equal(t, []int{3}, ids(replay), "reconnect must replay the event missed during the gap")
}

// (b) No duplicate at the replay/live boundary: replay stops exactly where live
// begins.
func TestAppEventLogNoDuplicateAtBoundary(t *testing.T) {
	l := newAppEventLog()
	for range 5 {
		l.ingest(ev()) // ids 1..5
	}

	replay, sub, unsub := l.subscribe(3)
	defer unsub()
	require.Equal(t, []int{4, 5}, ids(replay))

	l.ingest(ev()) // id 6 -> live only
	select {
	case e := <-sub.ch:
		require.Equal(t, 6, e.id)
	default:
		t.Fatal("expected live event 6 on the subscriber channel")
	}
	// 6 must not also have been in replay.
	require.NotContains(t, ids(replay), 6)
}

// (b, concurrent) A reconnecting subscribe with a cursor races the ingester, so
// the replay snapshot and the live fan-out interleave — the exact window a dup
// would appear in if fan-out were outside the append lock. Invariant: delivery
// is dup-free and hole-free, a contiguous run starting at cursor+1. If the
// drainer is descheduled the bounded buffer overflows and the connection drops
// (correct self-heal), truncating the tail — so we assert contiguity from
// cursor+1 up to max, no hole, no dup. Run with -race.
func TestAppEventLogConcurrentSubscribeIsGapAndDupFree(t *testing.T) {
	l := newAppEventLog()
	const (
		cursor = 10
		total  = 300
	)
	// Prime the ring so the cursor points into retained history; the concurrent
	// subscribe then replays part of the ring while the ingester keeps appending.
	for range 50 {
		l.ingest(ev())
	}

	var mu sync.Mutex
	seen := map[int]bool{}
	dup := -1
	record := func(id int) {
		mu.Lock()
		if seen[id] {
			dup = id
		}
		seen[id] = true
		mu.Unlock()
	}

	ingestDone := make(chan struct{})
	go func() {
		for i := 50; i < total; i++ {
			l.ingest(ev())
		}
		close(ingestDone)
	}()

	replay, sub, unsub := l.subscribe(cursor)
	defer unsub()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for _, e := range replay {
			record(e.id)
		}
		for {
			select {
			case e := <-sub.ch:
				record(e.id)
			case <-sub.dead:
				return
			case <-ingestDone:
				for {
					select {
					case e := <-sub.ch:
						record(e.id)
					default:
						return
					}
				}
			}
		}
	}()
	<-drained

	require.Equal(t, -1, dup, "duplicate id delivered across the replay/live boundary")
	max := 0
	for id := range seen {
		if id > max {
			max = id
		}
	}
	require.Greater(t, max, cursor)
	for id := cursor + 1; id <= max; id++ {
		require.True(t, seen[id], "hole at id %d below max %d (mid-stream gap)", id, max)
	}
	require.Len(t, seen, max-cursor, "delivered ids must be a contiguous run cursor+1..max")
}

// (c) A stale-higher-id (daemon restarted, seq reset, client holds a larger id)
// yields empty replay and a clean live start.
func TestAppEventLogStaleHigherIDStartsClean(t *testing.T) {
	l := newAppEventLog()
	l.ingest(ev())
	l.ingest(ev())
	l.ingest(ev()) // ids 1..3

	replay, sub, unsub := l.subscribe(99)
	defer unsub()
	require.Empty(t, replay)

	l.ingest(ev()) // id 4
	select {
	case e := <-sub.ch:
		require.Equal(t, 4, e.id)
	default:
		t.Fatal("expected live event 4")
	}
}

// (d) When the client was gone longer than the ring, replay starts at the
// oldest retained id, not at lastEventID+1 (the evicted gap can't be filled).
func TestAppEventLogEvictedOlderIDReplaysFromOldestRetained(t *testing.T) {
	l := newAppEventLog()
	total := appEventRingSize + 100
	for range total {
		l.ingest(ev())
	}

	replay, _, unsub := l.subscribe(2) // 2 is long evicted
	defer unsub()

	oldest := total - appEventRingSize + 1
	require.Len(t, replay, appEventRingSize)
	require.Equal(t, oldest, replay[0].id, "replay starts at the oldest retained id")
	require.Equal(t, total, replay[len(replay)-1].id)
}

// (e) A subscriber that falls behind is disconnected (dead closed) rather than
// silently dropping events.
func TestAppEventLogOverflowDisconnects(t *testing.T) {
	l := newAppEventLog()
	_, sub, unsub := l.subscribe(0)
	defer unsub()

	// Never drain sub.ch; push past the buffer.
	for range appSubBufSize + 5 {
		l.ingest(ev())
	}

	select {
	case <-sub.dead:
	default:
		t.Fatal("expected the overflowed subscriber to be disconnected")
	}
}

// (f) Resume precedence: header wins over query; empty header falls back to
// query; both empty starts from 0.
func TestResolveResumeID(t *testing.T) {
	require.Equal(t, 5, resolveResumeID("5", "9"))
	require.Equal(t, 9, resolveResumeID("", "9"))
	require.Equal(t, 9, resolveResumeID("  ", "9"))
	require.Equal(t, 9, resolveResumeID("garbage", "9"))
	require.Equal(t, 0, resolveResumeID("", ""))
	require.Equal(t, 0, resolveResumeID("-3", ""))
}
