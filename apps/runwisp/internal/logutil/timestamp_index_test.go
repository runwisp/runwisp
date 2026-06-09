// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildTidx(entries []TimestampEntry) *bytes.Reader {
	buf := make([]byte, len(entries)*TimestampIndexEntrySize)
	for i, e := range entries {
		WriteTimestampEntry(buf[i*TimestampIndexEntrySize:], e)
	}
	return bytes.NewReader(buf)
}

func TestTidxPath(t *testing.T) {
	assert.Equal(t, "/var/log/task/run.log.tidx", TidxPath("/var/log/task/run.log"))
	assert.Equal(t, ".tidx", TidxPath(""))
}

func TestTimestampEntryCount(t *testing.T) {
	assert.Equal(t, 0, TimestampEntryCount(0))
	assert.Equal(t, 1, TimestampEntryCount(12))
	assert.Equal(t, 2, TimestampEntryCount(24))
	assert.Equal(t, 2, TimestampEntryCount(30)) // partial record ignored
}

func TestWriteAndReadTimestampEntry(t *testing.T) {
	e := TimestampEntry{Line: 1024, Timestamp: 1700000000000}
	var buf [TimestampIndexEntrySize]byte
	WriteTimestampEntry(buf[:], e)

	r := bytes.NewReader(buf[:])
	got, err := ReadTimestampAt(r, 0)
	require.NoError(t, err)
	assert.Equal(t, e, got)
}

func TestReadTimestampAt_OutOfBounds(t *testing.T) {
	r := bytes.NewReader(nil)
	_, err := ReadTimestampAt(r, 0)
	assert.Error(t, err)
}

func TestReadTimestampAt_MultipleEntries(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 100, Timestamp: 2000},
		{Line: 200, Timestamp: 3000},
	}
	r := buildTidx(entries)

	for i, want := range entries {
		got, err := ReadTimestampAt(r, i)
		require.NoError(t, err)
		assert.Equal(t, want, got, "entry %d", i)
	}
}

func TestLookupTimestampByLine_Empty(t *testing.T) {
	r := bytes.NewReader(nil)
	ts, err := LookupTimestampByLine(r, 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(0), ts)
}

func TestLookupTimestampByLine_SingleEntry(t *testing.T) {
	r := buildTidx([]TimestampEntry{{Line: 0, Timestamp: 5000}})

	ts, err := LookupTimestampByLine(r, TimestampIndexEntrySize, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5000), ts)

	ts, err = LookupTimestampByLine(r, TimestampIndexEntrySize, 999)
	require.NoError(t, err)
	assert.Equal(t, int64(5000), ts)
}

func TestLookupTimestampByLine_ExactMatch(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 1024, Timestamp: 2000},
		{Line: 2048, Timestamp: 3000},
	}
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	ts, err := LookupTimestampByLine(r, size, 1024)
	require.NoError(t, err)
	assert.Equal(t, int64(2000), ts)
}

func TestLookupTimestampByLine_BetweenEntries(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 1024, Timestamp: 2000},
		{Line: 2048, Timestamp: 3000},
	}
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	// Line 500 is between entries 0 and 1024 -> returns timestamp of entry 0
	ts, err := LookupTimestampByLine(r, size, 500)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), ts)

	// Line 1500 is between 1024 and 2048 -> returns timestamp of entry 1024
	ts, err = LookupTimestampByLine(r, size, 1500)
	require.NoError(t, err)
	assert.Equal(t, int64(2000), ts)

	// Line 9999 is beyond all entries -> returns timestamp of last entry
	ts, err = LookupTimestampByLine(r, size, 9999)
	require.NoError(t, err)
	assert.Equal(t, int64(3000), ts)
}

func TestLookupTimestampByLine_BeforeFirstEntry(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 100, Timestamp: 5000},
	}
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	// Line 50 is before the first entry (line 100) -> no entry has Line <= 50
	ts, err := LookupTimestampByLine(r, size, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(0), ts)
}

func TestLookupLineRangeByTime_Empty(t *testing.T) {
	r := bytes.NewReader(nil)
	start, end, err := LookupLineRangeByTime(r, 0, 1000, 2000)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(0), end)
}

func TestLookupLineRangeByTime_FullRange(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 100, Timestamp: 2000},
		{Line: 200, Timestamp: 3000},
	}
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	start, end, err := LookupLineRangeByTime(r, size, 0, 5000)
	require.NoError(t, err)
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
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	start, end, err := LookupLineRangeByTime(r, size, 2000, 4000)
	require.NoError(t, err)
	assert.Equal(t, uint32(100), start)
	assert.Equal(t, uint32(300), end)
}

func TestLookupLineRangeByTime_NoMatch_AllBefore(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 100, Timestamp: 2000},
	}
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	start, end, err := LookupLineRangeByTime(r, size, 5000, 6000)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(0), end)
}

func TestLookupLineRangeByTime_NoMatch_AllAfter(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 0, Timestamp: 5000},
		{Line: 100, Timestamp: 6000},
	}
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	start, end, err := LookupLineRangeByTime(r, size, 1000, 2000)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(0), end)
}

func TestLookupLineRangeByTime_SingleEntry_InRange(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 42, Timestamp: 3000},
	}
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	start, end, err := LookupLineRangeByTime(r, size, 2000, 4000)
	require.NoError(t, err)
	assert.Equal(t, uint32(42), start)
	assert.Equal(t, uint32(42), end)
}

func TestLookupLineRangeByTime_SingleEntry_OutOfRange(t *testing.T) {
	entries := []TimestampEntry{
		{Line: 42, Timestamp: 3000},
	}
	r := buildTidx(entries)
	size := int64(len(entries) * TimestampIndexEntrySize)

	start, end, err := LookupLineRangeByTime(r, size, 5000, 6000)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), start)
	assert.Equal(t, uint32(0), end)
}

func TestReadTimestampIndex_File(t *testing.T) {
	dir := t.TempDir()
	tidxPath := dir + "/test.log.tidx"

	entries := []TimestampEntry{
		{Line: 0, Timestamp: 1000},
		{Line: 1024, Timestamp: 2000},
		{Line: 2048, Timestamp: 3000},
	}

	buf := make([]byte, len(entries)*TimestampIndexEntrySize)
	for i, e := range entries {
		WriteTimestampEntry(buf[i*TimestampIndexEntrySize:], e)
	}
	require.NoError(t, os.WriteFile(tidxPath, buf, 0644))

	got, err := ReadTimestampIndex(tidxPath)
	require.NoError(t, err)
	assert.Equal(t, entries, got)
}

func TestReadTimestampIndex_MissingFile(t *testing.T) {
	_, err := ReadTimestampIndex("/nonexistent/path.tidx")
	assert.Error(t, err)
}

func TestTimestampEntryEncoding(t *testing.T) {
	var buf [TimestampIndexEntrySize]byte
	e := TimestampEntry{Line: 0xDEADBEEF, Timestamp: 0x0102030405060708}
	WriteTimestampEntry(buf[:], e)

	assert.Equal(t, uint32(0xDEADBEEF), binary.LittleEndian.Uint32(buf[0:4]))
	assert.Equal(t, uint64(0x0102030405060708), binary.LittleEndian.Uint64(buf[4:12]))
}
