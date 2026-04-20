// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"encoding/binary"
	"io"
	"os"
)

const (
	// TimestampIndexEntrySize is the byte size of one .tidx record:
	// 4 bytes uint32 line number + 8 bytes int64 unix milliseconds.
	TimestampIndexEntrySize = 12
)

// TimestampEntry is one record in the .tidx timestamp index file.
type TimestampEntry struct {
	Line      uint32 // local line number within current log segment
	Timestamp int64  // unix milliseconds
}

// TidxPath returns the timestamp index path for a log file.
func TidxPath(logPath string) string {
	return logPath + ".tidx"
}

// TimestampEntryCount returns the number of complete entries from the file size.
// Partial trailing records (from crashes) are silently ignored.
func TimestampEntryCount(size int64) int {
	return int(size / TimestampIndexEntrySize)
}

// ReadTimestampAt reads a single entry at the given record index (0-based).
func ReadTimestampAt(r io.ReaderAt, index int) (TimestampEntry, error) {
	var buf [TimestampIndexEntrySize]byte
	_, err := r.ReadAt(buf[:], int64(index)*TimestampIndexEntrySize)
	if err != nil {
		return TimestampEntry{}, err
	}
	return TimestampEntry{
		Line:      binary.LittleEndian.Uint32(buf[0:4]),
		Timestamp: int64(binary.LittleEndian.Uint64(buf[4:12])),
	}, nil
}

// WriteTimestampEntry encodes a single entry into buf (must be >= 12 bytes).
func WriteTimestampEntry(buf []byte, e TimestampEntry) {
	binary.LittleEndian.PutUint32(buf[0:4], e.Line)
	binary.LittleEndian.PutUint64(buf[4:12], uint64(e.Timestamp))
}

// LookupTimestampByLine finds the approximate timestamp for a local line number
// using binary search. Returns the timestamp of the last entry with Line <= localLine.
// Returns 0 if the index is empty.
func LookupTimestampByLine(r io.ReaderAt, size int64, localLine uint32) (int64, error) {
	count := TimestampEntryCount(size)
	if count == 0 {
		return 0, nil
	}

	lo, hi := 0, count-1
	var result int64
	for lo <= hi {
		mid := lo + (hi-lo)/2
		e, err := ReadTimestampAt(r, mid)
		if err != nil {
			return 0, err
		}
		if e.Line <= localLine {
			result = e.Timestamp
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return result, nil
}

// LookupLineRangeByTime finds the approximate local line range for a time window
// using binary search. Returns the line of the first entry with Timestamp >= fromMs
// as startLine and the line of the last entry with Timestamp <= toMs as endLine.
// If no entries fall in range, returns (0, 0, nil).
func LookupLineRangeByTime(r io.ReaderAt, size int64, fromMs, toMs int64) (startLine, endLine uint32, err error) {
	count := TimestampEntryCount(size)
	if count == 0 {
		return 0, 0, nil
	}

	// Lower bound: first entry with Timestamp >= fromMs
	lo, hi := 0, count
	for lo < hi {
		mid := lo + (hi-lo)/2
		e, err := ReadTimestampAt(r, mid)
		if err != nil {
			return 0, 0, err
		}
		if e.Timestamp < fromMs {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= count {
		return 0, 0, nil
	}
	startEntry, err := ReadTimestampAt(r, lo)
	if err != nil {
		return 0, 0, err
	}
	if startEntry.Timestamp > toMs {
		return 0, 0, nil
	}
	startLine = startEntry.Line

	// Upper bound: last entry with Timestamp <= toMs
	lo2, hi2 := lo, count-1
	endLine = startLine
	for lo2 <= hi2 {
		mid := lo2 + (hi2-lo2)/2
		e, err := ReadTimestampAt(r, mid)
		if err != nil {
			return 0, 0, err
		}
		if e.Timestamp <= toMs {
			endLine = e.Line
			lo2 = mid + 1
		} else {
			hi2 = mid - 1
		}
	}
	return startLine, endLine, nil
}

// ReadTimestampIndex loads the entire .tidx file into memory.
// For large files, prefer ReadTimestampAt with binary search.
func ReadTimestampIndex(tidxPath string) ([]TimestampEntry, error) {
	f, err := os.Open(tidxPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	count := TimestampEntryCount(info.Size())
	entries := make([]TimestampEntry, count)
	for i := range count {
		e, err := ReadTimestampAt(f, i)
		if err != nil {
			return nil, err
		}
		entries[i] = e
	}
	return entries, nil
}
