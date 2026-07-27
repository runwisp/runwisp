// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import (
	"encoding/binary"
	"sort"
)

const (
	// TimestampIndexEntrySize is the byte size of one timestamp record:
	// 4 bytes uint32 line number + 8 bytes int64 unix milliseconds.
	TimestampIndexEntrySize = 12
)

// TimestampEntry is one record in the timestamp index. Entries are stored in the
// consolidated sidecar container as `t` records.
type TimestampEntry struct {
	Line      uint32 // local line number within current log segment
	Timestamp int64  // unix milliseconds
}

// WriteTimestampEntry encodes a single entry into buf (must be >= 12 bytes).
func WriteTimestampEntry(buf []byte, e TimestampEntry) {
	binary.LittleEndian.PutUint32(buf[0:4], e.Line)
	binary.LittleEndian.PutUint64(buf[4:12], uint64(e.Timestamp))
}

// LookupTimestampByLine returns the timestamp of the last entry with
// Line <= localLine, or 0 when the index is empty. Entries are ordered by line.
func LookupTimestampByLine(entries []TimestampEntry, localLine uint32) int64 {
	// First index i where entries[i].Line > localLine; the answer is i-1.
	i := sort.Search(len(entries), func(i int) bool {
		return entries[i].Line > localLine
	})
	if i == 0 {
		return 0
	}
	return entries[i-1].Timestamp
}

// LookupLineRangeByTime returns the approximate local line range whose entries
// fall within [fromMs, toMs]. startLine is the line of the first entry with
// Timestamp >= fromMs; endLine is the line of the last entry with
// Timestamp <= toMs. Returns (0, 0) when no entry falls in range. Entries are
// ordered by timestamp.
func LookupLineRangeByTime(entries []TimestampEntry, fromMs, toMs int64) (startLine, endLine uint32) {
	lo := sort.Search(len(entries), func(i int) bool {
		return entries[i].Timestamp >= fromMs
	})
	if lo >= len(entries) || entries[lo].Timestamp > toMs {
		return 0, 0
	}
	startLine = entries[lo].Line
	endLine = startLine
	for i := lo; i < len(entries) && entries[i].Timestamp <= toMs; i++ {
		endLine = entries[i].Line
	}
	return startLine, endLine
}
