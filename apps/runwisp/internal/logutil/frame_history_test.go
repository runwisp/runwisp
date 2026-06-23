// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFhist(t *testing.T, logPath string, groups map[int64][][]string, order []int64) {
	t.Helper()
	f, err := os.Create(FhistPath(logPath))
	require.NoError(t, err)
	defer f.Close()
	for _, anchor := range order {
		rec, err := EncodeFrameHistoryEntry(anchor, groups[anchor])
		require.NoError(t, err)
		_, err = f.Write(rec)
		require.NoError(t, err)
	}
}

func TestFhistPath(t *testing.T) {
	assert.Equal(t, "/var/log/task/run.log.fhist", FhistPath("/var/log/task/run.log"))
}

func TestFrameHistoryRoundTrip(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run.log")
	groups := map[int64][][]string{
		3:  {{"build 0%"}, {"build 50%"}},
		7:  {{"a 0%", "b 0%", "c 0%"}, {"a 50%", "b 0%", "c 0%"}},
		12: {{"only one"}},
	}
	writeFhist(t, logPath, groups, []int64{3, 7, 12})

	// Single-row frames.
	frames, ok := ReadFrameHistory(logPath, 3)
	require.True(t, ok)
	assert.Equal(t, [][]string{{"build 0%"}, {"build 50%"}}, frames)

	// Whole multi-row frames stay grouped per instant.
	frames, ok = ReadFrameHistory(logPath, 7)
	require.True(t, ok)
	assert.Equal(t, [][]string{{"a 0%", "b 0%", "c 0%"}, {"a 50%", "b 0%", "c 0%"}}, frames)

	// Unknown anchor.
	_, ok = ReadFrameHistory(logPath, 99)
	assert.False(t, ok)

	counts := ReadFrameHistoryCounts(logPath)
	assert.Equal(t, map[int64]int{3: 2, 7: 2, 12: 1}, counts)
}

func TestFrameHistoryMissingFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "absent.log")
	frames, ok := ReadFrameHistory(logPath, 0)
	assert.False(t, ok)
	assert.Nil(t, frames)
	assert.Empty(t, ReadFrameHistoryCounts(logPath))
}

func TestFrameHistorySkipsTornTrailingLine(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run.log")
	writeFhist(t, logPath, map[int64][][]string{5: {{"good"}}}, []int64{5})

	// Append a torn/garbage trailing line, as a kill -9 mid-write might leave.
	f, err := os.OpenFile(FhistPath(logPath), os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"n":9,"frames":[["partial`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	frames, ok := ReadFrameHistory(logPath, 5)
	require.True(t, ok)
	assert.Equal(t, [][]string{{"good"}}, frames)

	_, ok = ReadFrameHistory(logPath, 9)
	assert.False(t, ok)

	assert.Equal(t, map[int64]int{5: 1}, ReadFrameHistoryCounts(logPath))
}
