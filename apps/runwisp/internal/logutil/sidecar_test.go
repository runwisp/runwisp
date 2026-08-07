// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeContainer appends the given framed records to a run's container.
func writeContainer(t *testing.T, logPath string, recs ...[]byte) {
	t.Helper()
	f, err := os.Create(MetaPath(logPath))
	require.NoError(t, err)
	defer f.Close()
	for _, rec := range recs {
		_, err := f.Write(rec)
		require.NoError(t, err)
	}
}

func TestReadSidecar_Missing(t *testing.T) {
	sc := ReadSidecar(filepath.Join(t.TempDir(), "absent.log"))
	assert.Equal(t, LogMeta{}, sc.Meta)
	assert.Empty(t, sc.Index)
	assert.Empty(t, sc.Frames)
}

func TestReadSidecar_AllRecordTypes(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run.log")
	frameRec, err := FrameRecord(7, [][]string{{"a"}, {"b"}})
	require.NoError(t, err)
	writeContainer(t, logPath,
		IndexRecord(0),
		IndexRecord(4096),
		frameRec,
		MetaRecord(LogMeta{RotatedLines: 5, RotatedBytes: 50, FinalLines: 10, Finalized: true}),
	)

	sc := ReadSidecar(logPath)
	assert.Equal(t, []int64{0, 4096}, sc.Index)
	assert.Equal(t, [][]string{{"a"}, {"b"}}, sc.Frames[7])
	assert.Equal(t, LogMeta{RotatedLines: 5, RotatedBytes: 50, FinalLines: 10, Finalized: true}, sc.Meta)
}

func TestReadSidecar_LastMetaWins(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run.log")
	writeContainer(t, logPath,
		MetaRecord(LogMeta{RotatedLines: 1, RotatedBytes: 10}),
		MetaRecord(LogMeta{RotatedLines: 2, RotatedBytes: 20, FinalLines: 99, Finalized: true}),
	)
	assert.Equal(t, LogMeta{RotatedLines: 2, RotatedBytes: 20, FinalLines: 99, Finalized: true}, ReadSidecar(logPath).Meta)
}

func TestReadSidecar_StopsAtTornTrailingRecord(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run.log")
	writeContainer(t, logPath, MetaRecord(LogMeta{RotatedLines: 3}), IndexRecord(0))

	// Append a header claiming a payload longer than what follows.
	f, err := os.OpenFile(MetaPath(logPath), os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, err = f.Write([]byte{recIndex, 0x10, 0x00, 0x00, 0x00, 'a', 'b'})
	require.NoError(t, err)
	require.NoError(t, f.Close())

	sc := ReadSidecar(logPath)
	assert.Equal(t, int64(3), sc.Meta.RotatedLines)
	assert.Equal(t, []int64{0}, sc.Index, "torn trailing record must be ignored")
}

func TestReadSidecar_OversizedRecordStops(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run.log")
	// A header whose length exceeds maxSidecarRecord is treated as corruption.
	writeContainer(t, logPath, []byte{recFrame, 0xFF, 0xFF, 0xFF, 0xFF})
	sc := ReadSidecar(logPath)
	assert.Empty(t, sc.Frames)
}
