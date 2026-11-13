// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil/fakeclock"
	"github.com/stretchr/testify/require"
)

// ev builds a minimal bus event; ingest only cares about identity/order, not
// the payload, so the type is arbitrary.
func ev() events.Event {
	return events.Event{Type: events.EventRunStarted, Data: events.RunEvent{}}
}

// newTestEventLog seeds ids from the Unix epoch so they run 1..n, keeping the
// ordering assertions below readable.
func newTestEventLog() *appEventLog {
	return newAppEventLog(fakeclock.New(time.Unix(0, 0)).Now)
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
	l := newTestEventLog()
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
	l := newTestEventLog()
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
	l := newTestEventLog()
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
	l := newTestEventLog()
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
	maxID := 0
	for id := range seen {
		if id > maxID {
			maxID = id
		}
	}
	require.Greater(t, maxID, cursor)
	for id := cursor + 1; id <= maxID; id++ {
		require.True(t, seen[id], "hole at id %d below max %d (mid-stream gap)", id, maxID)
	}
	require.Len(t, seen, maxID-cursor, "delivered ids must be a contiguous run cursor+1..max")
}

// (c) A stale-higher-id (daemon restarted, seq reset, client holds a larger id)
// yields empty replay and a clean live start.
func TestAppEventLogStaleHigherIDStartsClean(t *testing.T) {
	l := newTestEventLog()
	l.ingest(ev())
	l.ingest(ev())
	l.ingest(ev()) // ids 1..3

	replay, sub, unsub := l.subscribe(99)
	defer unsub()
	require.Empty(t, replay)
	require.True(t, sub.resync, "a cursor past the head means another boot; the client must resync")

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
	l := newTestEventLog()
	total := appEventRingSize + 100
	for range total {
		l.ingest(ev())
	}

	replay, sub, unsub := l.subscribe(2) // 2 is long evicted
	defer unsub()
	require.True(t, sub.resync, "evicted cursor means events were lost; the client must resync")

	oldest := total - appEventRingSize + 1
	require.Len(t, replay, appEventRingSize)
	require.Equal(t, oldest, replay[0].id, "replay starts at the oldest retained id")
	require.Equal(t, total, replay[len(replay)-1].id)
}

// (e) A subscriber that falls behind is disconnected (dead closed) rather than
// silently dropping events.
func TestAppEventLogOverflowDisconnects(t *testing.T) {
	l := newTestEventLog()
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

// A daemon restart must not reuse ids: ids are seeded from the boot clock, so
// a later boot always starts above anything an earlier boot handed out, and
// stays inside JS's safe-integer range.
func TestAppEventLogIDsUniqueAcrossBoots(t *testing.T) {
	boot1 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	l1 := newAppEventLog(fakeclock.New(boot1).Now)
	for range 5000 {
		l1.ingest(ev())
	}
	_, sub1, unsub1 := l1.subscribe(0)
	defer unsub1()
	l1.ingest(ev())
	last1 := (<-sub1.ch).id

	boot2 := boot1.Add(time.Second)
	l2 := newAppEventLog(fakeclock.New(boot2).Now)
	_, sub2, unsub2 := l2.subscribe(0)
	defer unsub2()
	l2.ingest(ev())
	first2 := (<-sub2.ch).id

	require.Greater(t, first2, last1, "second boot must issue ids above every id of the first")
	require.Equal(t, int(boot2.UnixMicro())+1, first2)
	require.Less(t, first2, 1<<53, "ids must stay JS-safe integers")
}

// A client holding an id from the previous boot reconnects to a new daemon:
// it must be told to resync rather than silently receiving nothing.
func TestAppEventLogCursorFromPreviousBootResyncs(t *testing.T) {
	boot1 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	cursor := int(boot1.UnixMicro()) + 5000
	boot2 := boot1.Add(time.Minute)

	l2 := newAppEventLog(fakeclock.New(boot2).Now)
	l2.ingest(ev())
	_, sub, unsub := l2.subscribe(cursor)
	defer unsub()
	require.True(t, sub.resync)

	// Same, before the new boot has emitted anything (empty ring).
	l3 := newAppEventLog(fakeclock.New(boot2).Now)
	_, sub3, unsub3 := l3.subscribe(cursor)
	defer unsub3()
	require.True(t, sub3.resync, "empty ring must not hide a stale cursor")
}

func TestAppEventLogResyncDecision(t *testing.T) {
	// 1100 events on a 1024 ring: seq=1100, retained ids 77..1100.
	full := func() *appEventLog {
		l := newTestEventLog()
		for range appEventRingSize + 76 {
			l.ingest(ev())
		}
		return l
	}
	cases := []struct {
		name   string
		log    func() *appEventLog
		cursor int
		resync bool
	}{
		{"no cursor", full, 0, false},
		{"at head", full, 1100, false},
		{"mid ring", full, 500, false},
		{"oldest retained minus one (nothing missed)", full, 76, false},
		{"evicted", full, 75, true},
		{"past head", full, 1101, true},
		{"empty ring, no cursor", newTestEventLog, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, sub, unsub := tc.log().subscribe(tc.cursor)
			defer unsub()
			require.Equal(t, tc.resync, sub.resync)
		})
	}
}
