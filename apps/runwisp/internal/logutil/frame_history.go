// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

// Frame history records the prior states a settled progress bar or multi-line
// redraw animated through. It is sparse: it exists only for runs that produced
// in-place output. Each record is one finalized commit group, stored in the
// consolidated sidecar container as an `f` record:
//
//	{"n":<anchor>,"frames":[["row0","row1",...],...]}
//
// where `anchor` is the first committed line number of the group and each frame
// is the whole region at one instant (multi-row redraws are kept whole). The
// committed final state is not repeated in frames. History is best-effort and
// supplementary: losing it (rotation, kill -9, a torn trailing write) never
// affects the durable `.log`.

// frameHistoryEntry is one finalized commit group's history record.
type frameHistoryEntry struct {
	N      int64      `json:"n"`
	Frames [][]string `json:"frames"`
}

// ReadFrameHistory returns the whole-region frames recorded for the commit group
// anchored at line n. The second result is false when no entry exists for n.
func ReadFrameHistory(logPath string, n int64) ([][]string, bool) {
	frames, ok := ReadSidecar(logPath).Frames[n]
	return frames, ok
}

// ReadFrameHistoryCounts maps each anchor line number to how many frames it
// recorded, for annotating a page of log lines with their history availability.
// Returns an empty (non-nil) map when no frame history exists.
func ReadFrameHistoryCounts(logPath string) map[int64]int {
	frames := ReadSidecar(logPath).Frames
	counts := make(map[int64]int, len(frames))
	for anchor, f := range frames {
		counts[anchor] = len(f)
	}
	return counts
}
