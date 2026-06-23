// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// Frame history (`.fhist`) is a sparse, append-only JSONL sidecar holding the
// prior states a settled progress bar or multi-line redraw animated through. It
// exists only for runs that produced in-place output; a normal log has no
// `.fhist` file at all. Each line is one finalized commit group:
//
//	{"n":<anchor>,"frames":[["row0","row1",...],...]}
//
// where `anchor` is the first committed line number of the group and each frame
// is the whole region at one instant (multi-row redraws are kept whole). The
// committed final state is not repeated in frames. History is best-effort and
// supplementary: losing it (rotation, kill -9, a torn trailing write) never
// affects the durable `.log`.

// FhistPath returns the frame-history sidecar path for a log file.
func FhistPath(logPath string) string {
	return logPath + ".fhist"
}

// frameHistoryEntry is one finalized commit group's history line.
type frameHistoryEntry struct {
	N      int64      `json:"n"`
	Frames [][]string `json:"frames"`
}

// EncodeFrameHistoryEntry marshals one group's frame history as a single JSONL
// record (newline-terminated), ready to append to the `.fhist` sidecar.
func EncodeFrameHistoryEntry(anchor int64, frames [][]string) ([]byte, error) {
	b, err := json.Marshal(frameHistoryEntry{N: anchor, Frames: frames})
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ReadFrameHistory returns the whole-region frames recorded for the commit group
// anchored at line n. The second result is false when the sidecar is absent or
// has no entry for n. A torn or unparseable trailing line is skipped. When a
// line was rewritten (it never is today — entries are append-only and unique per
// anchor), the last matching entry wins.
func ReadFrameHistory(logPath string, n int64) ([][]string, bool) {
	var frames [][]string
	found := false
	forEachFrameHistoryEntry(logPath, func(e frameHistoryEntry) {
		if e.N == n {
			frames = e.Frames
			found = true
		}
	})
	return frames, found
}

// ReadFrameHistoryCounts maps each anchor line number to how many frames it
// recorded, for annotating a page of log lines with their history availability.
// Returns an empty (non-nil) map quickly when the sidecar is absent.
func ReadFrameHistoryCounts(logPath string) map[int64]int {
	counts := make(map[int64]int)
	forEachFrameHistoryEntry(logPath, func(e frameHistoryEntry) {
		counts[e.N] = len(e.Frames)
	})
	return counts
}

// forEachFrameHistoryEntry scans the `.fhist` sidecar line by line, invoking fn
// for each well-formed entry. A missing file is not an error. Unparseable lines
// (e.g. a torn trailing write after kill -9) are skipped silently.
func forEachFrameHistoryEntry(logPath string, fn func(frameHistoryEntry)) {
	f, err := os.Open(FhistPath(logPath))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			// Best-effort sidecar: a read error degrades to "no history".
			return
		}
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Frames can be large (many rows, each a full terminal width); allow a
	// generous per-line buffer so a legitimate group is not silently dropped.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e frameHistoryEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // torn or garbage line; skip
		}
		fn(e)
	}
}
