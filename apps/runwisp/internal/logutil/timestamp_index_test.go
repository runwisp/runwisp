// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLookupTimestampByLine_Empty(t *testing.T) {
	assert.Equal(t, int64(0), LookupTimestampByLine(nil, 50))
}

func TestLookupTimestampByLine_SingleEntry(t *testing.T) {
	entries := []TimestampEntry{{Line: 0, Timestamp: 5000}}
	assert.Equal(t, int64(5000), LookupTimestampByLine(entries, 0))
	assert.Equal(t, int64(5000), LookupTimestampByLine(entries, 999))
}

func TestLookupTimestampByLine_ExactMatch(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 1024, Timestamp: 2000},
		{Line: 2048, Timestamp: 3000},
	}
	assert.Equal(t, int64(2000), LookupTimestampByLine(entries, 1024))
}

func TestLookupTimestampByLine_BetweenEntries(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 1024, Timestamp: 2000},
		{Line: 2048, Timestamp: 3000},
	}
	// Line 500 is between entries 0 and 1024 -> returns timestamp of entry 0.
	assert.Equal(t, int64(1000), LookupTimestampByLine(entries, 500))
	// Line 1500 is between 1024 and 2048 -> returns timestamp of entry 1024.
	assert.Equal(t, int64(2000), LookupTimestampByLine(entries, 1500))
	// Line 9999 is beyond all entries -> returns timestamp of last entry.
	assert.Equal(t, int64(3000), LookupTimestampByLine(entries, 9999))
}

func TestLookupTimestampByLine_BeforeFirstEntry(t *testing.T) {
	// Line 50 is before the first entry (line 100) -> no entry has Line <= 50.
	entries := []TimestampEntry{{Line: 100, Timestamp: 5000}}
	assert.Equal(t, int64(0), LookupTimestampByLine(entries, 50))
}

func TestLookupLineRangeByTime_Empty(t *testing.T) {
	start, end := LookupLineRangeByTime(nil, 1000, 2000)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(0), end)
}

func TestLookupLineRangeByTime_FullRange(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 100, Timestamp: 2000},
		{Line: 200, Timestamp: 3000},
	}
	start, end := LookupLineRangeByTime(entries, 0, 5000)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(200), end)
}

func TestLookupLineRangeByTime_PartialRange(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 100, Timestamp: 2000},
		{Line: 200, Timestamp: 3000},
		{Line: 300, Timestamp: 4000},
		{Line: 400, Timestamp: 5000},
	}
	start, end := LookupLineRangeByTime(entries, 2000, 4000)
	assert.Equal(t, uint32(100), start)
	assert.Equal(t, uint32(300), end)
}

func TestLookupLineRangeByTime_NoMatch_AllBefore(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 100, Timestamp: 2000},
	}
	start, end := LookupLineRangeByTime(entries, 5000, 6000)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(0), end)
}

func TestLookupLineRangeByTime_NoMatch_AllAfter(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 5000},
		{Line: 100, Timestamp: 6000},
	}
	start, end := LookupLineRangeByTime(entries, 1000, 2000)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(0), end)
}

func TestLookupLineRangeByTime_SingleEntry_InRange(t *testing.T) {
	entries := []TimestampEntry{{Line: 42, Timestamp: 3000}}
	start, end := LookupLineRangeByTime(entries, 2000, 4000)
	assert.Equal(t, uint32(42), start)
	assert.Equal(t, uint32(42), end)
}

func TestLookupLineRangeByTime_SingleEntry_OutOfRange(t *testing.T) {
	entries := []TimestampEntry{{Line: 42, Timestamp: 3000}}
	start, end := LookupLineRangeByTime(entries, 5000, 6000)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(0), end)
}

func TestTimestampEntryEncoding(t *testing.T) {
	var buf [TimestampIndexEntrySize]byte
	e := TimestampEntry{Line: 0xDEADBEEF, Timestamp: 0x0102030405060708}
	WriteTimestampEntry(buf[:], e)

	assert.Equal(t, uint32(0xDEADBEEF), binary.LittleEndian.Uint32(buf[0:4]))
	assert.Equal(t, uint64(0x0102030405060708), binary.LittleEndian.Uint64(buf[4:12]))
}
