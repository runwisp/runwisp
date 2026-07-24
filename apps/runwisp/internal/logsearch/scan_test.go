// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logsearch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeLog creates path with the given lines, newline-terminated.
func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanRun_FindsMatchesInOrder(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	writeLog(t, logPath,
		"hello world",
		"connection refused",
		"goodbye",
		"another connection refused",
	)
	m, _ := NewMatcher("connection refused", false, false)
	hits, _, err := ScanRun(context.Background(), RunRef{
		ID:        "01HRUNAAAAAAAAAAAAAAAAAAAA",
		LogPath:   logPath,
		CreatedAt: time.Unix(1700000000, 0),
	}, m, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	if hits[0].N != 1 || hits[1].N != 3 {
		t.Fatalf("expected line numbers [1, 3], got [%d, %d]", hits[0].N, hits[1].N)
	}
	if hits[0].TS != time.Unix(1700000000, 0).UnixMilli() {
		t.Fatal("ts should match run.CreatedAt")
	}
}

func TestScanRun_RespectsMaxHits(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "foo line"
	}
	writeLog(t, logPath, lines...)
	m, _ := NewMatcher("foo", false, false)
	hits, more, err := ScanRun(context.Background(), RunRef{LogPath: logPath}, m, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 {
		t.Fatalf("want 5 hits, got %d", len(hits))
	}
	if !more {
		t.Fatal("expected more=true when hit cap was reached")
	}
}

func TestScanRun_StartAfterN(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "a.log")
	writeLog(t, logPath, "foo", "foo", "foo")
	m, _ := NewMatcher("foo", false, false)
	hits, _, err := ScanRun(context.Background(), RunRef{LogPath: logPath}, m, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(hits))
	}
	if hits[0].N != 2 {
		t.Fatalf("want N=2, got %d", hits[0].N)
	}
}

func TestScanTask_NewestFirstAndCursor(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	b := filepath.Join(dir, "b.log")
	writeLog(t, a, "foo line 1", "foo line 2")
	writeLog(t, b, "foo line 3")

	runs := []RunRef{
		// newer first
		{ID: "01HRUNAAAAAAAAAAAAAAAAAAAB", LogPath: b, CreatedAt: time.Unix(2, 0)},
		{ID: "01HRUNAAAAAAAAAAAAAAAAAAAA", LogPath: a, CreatedAt: time.Unix(1, 0)},
	}

	factory := func() Matcher {
		m, _ := NewMatcher("foo", false, false)
		return m
	}
	hits, cur, scanned, err := ScanTask(context.Background(), runs, factory, ScanOpts{MaxHits: 2}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 2 {
		t.Fatalf("want scanned=2, got %d", scanned)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	// Sorted newest-first by run TS, ascending line within run.
	if hits[0].RunID != "01HRUNAAAAAAAAAAAAAAAAAAAB" {
		t.Fatalf("want newest run first, got %s", hits[0].RunID)
	}
	if cur == nil {
		t.Fatal("expected cursor since maxHits cut into run A")
	}
	if cur.RunID != "01HRUNAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("cursor should point at run A, got %s", cur.RunID)
	}
}

func TestScanTask_CursorResumes(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	writeLog(t, a, "foo", "foo", "foo", "foo")

	runs := []RunRef{{ID: "R1", LogPath: a, CreatedAt: time.Unix(1, 0)}}
	factory := func() Matcher {
		m, _ := NewMatcher("foo", false, false)
		return m
	}
	hits, cur, _, err := ScanTask(context.Background(), runs, factory, ScanOpts{MaxHits: 2}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if cur == nil || cur.NextN != hits[1].N {
		t.Fatalf("expected cursor at last consumed line, got %+v", cur)
	}
	// Resume.
	hits2, _, _, err := ScanTask(context.Background(), runs, factory, ScanOpts{MaxHits: 2}, cur.RunID, cur.NextN)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits2) != 2 {
		t.Fatalf("resume should produce 2 more hits, got %d", len(hits2))
	}
	// One more resume should yield no hits — the cursor from page 2 may
	// be a false positive when the run ended exactly at maxHits, and the
	// client tolerates the empty page.
	hits3, _, _, err := ScanTask(context.Background(), runs, factory, ScanOpts{MaxHits: 2}, "R1", hits2[1].N)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits3) != 0 {
		t.Fatalf("expected zero hits after run exhausted, got %d", len(hits3))
	}
}

// TestScanTask_ExactFillAtRunBoundaryEmitsCursor guards the regression where
// the budget filled exactly at a run boundary: earlier code broke out of the
// flatten loop with no cursor, silently dropping the remaining (already
// scanned) runs in the window. The scan must emit a cursor pointing at the
// next run so the client can fetch the dropped hits.
func TestScanTask_ExactFillAtRunBoundaryEmitsCursor(t *testing.T) {
	dir := t.TempDir()
	b := filepath.Join(dir, "b.log")
	a := filepath.Join(dir, "a.log")
	c := filepath.Join(dir, "c.log")
	// B and A each contribute exactly one hit, filling maxHits=2 at A's
	// boundary (both more=false). C is still pending in the window and holds
	// matches. Old code broke out with no cursor, silently dropping C.
	writeLog(t, b, "foo b1", "skip")
	writeLog(t, a, "foo a1", "skip")
	writeLog(t, c, "foo c1", "foo c2")

	runs := []RunRef{
		{ID: "01HRUNAAAAAAAAAAAAAAAAAAAC", LogPath: b, CreatedAt: time.Unix(3, 0)},
		{ID: "01HRUNAAAAAAAAAAAAAAAAAAAB", LogPath: a, CreatedAt: time.Unix(2, 0)},
		{ID: "01HRUNAAAAAAAAAAAAAAAAAAAA", LogPath: c, CreatedAt: time.Unix(1, 0)},
	}
	factory := func() Matcher {
		m, _ := NewMatcher("foo", false, false)
		return m
	}

	hits, cur, _, err := ScanTask(context.Background(), runs, factory, ScanOpts{MaxHits: 2}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d", len(hits))
	}
	if cur == nil {
		t.Fatal("expected a cursor: run C was scanned but not emitted")
	}
	if cur.RunID != "01HRUNAAAAAAAAAAAAAAAAAAAA" || cur.NextN != 0 {
		t.Fatalf("cursor should resume run C from line 0, got %+v", cur)
	}

	// Resuming from that cursor must yield run C's hits.
	hits2, _, _, err := ScanTask(context.Background(), runs, factory, ScanOpts{MaxHits: 2}, cur.RunID, cur.NextN)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits2) != 2 || hits2[0].RunID != "01HRUNAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("resume should return run C's 2 hits, got %d", len(hits2))
	}
}

func TestScanTask_CancelMidScan(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	lines := make([]string, 10000)
	for i := range lines {
		lines[i] = "foo"
	}
	writeLog(t, a, lines...)
	runs := []RunRef{{ID: "R1", LogPath: a, CreatedAt: time.Unix(1, 0)}}
	factory := func() Matcher {
		m, _ := NewMatcher("foo", false, false)
		return m
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := ScanTask(ctx, runs, factory, ScanOpts{MaxHits: 1000}, "", 0)
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
