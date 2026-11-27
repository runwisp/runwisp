// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
)

// grepBenchSession is a session whose frames are discarded.
func grepBenchSession(b *testing.B) *session {
	b.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	s := &session{ctx: ctx, out: make(chan outFrame, 16), params: sessionParams{maxFrameBytes: defaultMaxFrameBytes}, subs: map[string]context.CancelFunc{}}
	go func() {
		for {
			select {
			case <-s.out:
			case <-ctx.Done():
				return
			}
		}
	}()
	return s
}

// writeBenchLog writes a log of about size bytes of mixed-case lines, some
// of them stderr, and the sidecar a finished run leaves, and returns the
// log's size.
func writeBenchLog(b *testing.B, path string, size int64) int64 {
	b.Helper()
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	var n int64
	for i := 0; n < size; i++ {
		var line string
		if i%7 == 0 {
			line = fmt.Sprintf("[ERR] 2026-03-04T10:00:%02d WARN worker %d: Retrying request to upstream after timeout\n", i%60, i)
		} else {
			line = fmt.Sprintf("2026-03-04T10:00:%02d INFO worker %d: Processed batch of %d items in %d ms\n", i%60, i, i%500, i%97)
		}
		k, _ := w.WriteString(line)
		n += int64(k)
	}
	if err := w.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
	meta := logutil.MetaRecord(logutil.LogMeta{Finalized: true})
	if err := os.WriteFile(logutil.MetaPath(path), meta, 0o600); err != nil {
		b.Fatal(err)
	}
	return n
}

// benchGrep scans runs once per iteration for a term no line holds, so every
// byte is read and matched.
func benchGrep(b *testing.B, paths []string, total int64) {
	s := grepBenchSession(b)
	req := protocol.LogSearch{Terms: []protocol.LogSearchTerm{word("nomatchhere")}, MaxHits: 500, TimeoutMs: 28000, Context: 2}
	b.SetBytes(total)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		g, err := s.newGrepScan("bench", req)
		if err != nil {
			b.Fatal(err)
		}
		for _, path := range paths {
			if err := g.scanRun(b.Context(), "bench", path, 0); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(len(paths)*b.N)/b.Elapsed().Seconds(), "runs/s")
}

// BenchmarkGrepSmallRuns scans 2000 runs of 8 KB, the median run.
func BenchmarkGrepSmallRuns(b *testing.B) {
	dir := b.TempDir()
	paths := make([]string, 2000)
	var total int64
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("run-%d.log", i))
		total += writeBenchLog(b, paths[i], 8<<10)
	}
	benchGrep(b, paths, total)
}

// BenchmarkGrepLargeLog scans one 256 MiB log; MB/s holds for larger ones.
func BenchmarkGrepLargeLog(b *testing.B) {
	path := filepath.Join(b.TempDir(), "big.log")
	total := writeBenchLog(b, path, 256<<20)
	benchGrep(b, []string{path}, total)
}
