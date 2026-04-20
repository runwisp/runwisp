// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempFileWithContent(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "logtest-*")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	_, err = f.Seek(0, 0)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

func TestScanOffset_CountAllLines(t *testing.T) {
	f := tempFileWithContent(t, "line1\nline2\nline3\n")
	_, lines, err := ScanOffset(f, 0, -1)
	require.NoError(t, err)
	assert.Equal(t, 3, lines)
}

func TestScanOffset_SkipLines(t *testing.T) {
	f := tempFileWithContent(t, "aaa\nbbb\nccc\nddd\n")
	offset, lines, err := ScanOffset(f, 0, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, lines)
	assert.Equal(t, int64(8), offset) // "aaa\n" (4) + "bbb\n" (4) = 8
}

func TestScanOffset_FromMiddle(t *testing.T) {
	content := "aaa\nbbb\nccc\n"
	f := tempFileWithContent(t, content)
	// Start after "aaa\n" (offset 4), skip 1 line ("bbb\n")
	offset, lines, err := ScanOffset(f, 4, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, lines)
	assert.Equal(t, int64(8), offset)
}

func TestScanOffset_EmptyFile(t *testing.T) {
	f := tempFileWithContent(t, "")
	_, lines, err := ScanOffset(f, 0, -1)
	require.NoError(t, err)
	assert.Equal(t, 0, lines)
}

func TestScanOffset_SkipBeyondEnd(t *testing.T) {
	f := tempFileWithContent(t, "one\n")
	_, lines, err := ScanOffset(f, 0, 5)
	assert.Equal(t, 1, lines)
	assert.ErrorIs(t, err, io.EOF)
}

func TestCalculateTotalLines_Finalized(t *testing.T) {
	f := tempFileWithContent(t, "unused")
	meta := LogMeta{Finalized: true, RotatedLines: 10, FinalLines: 5}
	total := CalculateTotalLines(f, nil, 6, meta)
	assert.Equal(t, 15, total)
}

func TestCalculateTotalLines_NoIndex(t *testing.T) {
	f := tempFileWithContent(t, "a\nb\nc\n")
	meta := LogMeta{RotatedLines: 2}
	total := CalculateTotalLines(f, nil, 6, meta)
	assert.Equal(t, 5, total) // 2 rotated + 3 current
}

func TestCalculateTotalLines_WithIndex(t *testing.T) {
	// 2050 lines, index entry every 1024 lines → indices[0]=0, indices[1]=offset of line 1024
	var lines []string
	for i := 0; i < 2050; i++ {
		lines = append(lines, "x")
	}
	content := strings.Join(lines, "\n") + "\n"
	f := tempFileWithContent(t, content)

	// Build a simple index: index[0] = 0, index[1] = offset of line 1024
	// Each line is "x\n" = 2 bytes
	indices := []int64{0, int64(1024 * 2)}
	total := CalculateTotalLines(f, indices, int64(len(content)), LogMeta{})
	assert.Equal(t, 2050, total)
}

func TestCalculateLineOffset_NoIndex(t *testing.T) {
	f := tempFileWithContent(t, "aaa\nbbb\nccc\n")
	meta := LogMeta{}
	offset := CalculateLineOffset(f, nil, 2, meta)
	assert.Equal(t, int64(8), offset) // skip 2 lines: "aaa\n"(4) + "bbb\n"(4) = 8
}

func TestCalculateLineOffset_WithRotation(t *testing.T) {
	f := tempFileWithContent(t, "ccc\nddd\n")
	meta := LogMeta{RotatedLines: 5}
	// Requesting global line 6 → local line 1
	offset := CalculateLineOffset(f, nil, 6, meta)
	assert.Equal(t, int64(4), offset) // skip "ccc\n"
}

func TestCalculateLineOffset_BeforeRotation(t *testing.T) {
	f := tempFileWithContent(t, "ccc\nddd\n")
	meta := LogMeta{RotatedLines: 5}
	// Requesting line 2 (before rotation start) → clamped to local line 0
	offset := CalculateLineOffset(f, nil, 2, meta)
	assert.Equal(t, int64(0), offset)
}

func TestParseLogOffset(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		fileSize int64
		want     int64
	}{
		{"empty", "", 100, 0},
		{"positive", "50", 100, 50},
		{"beyond end", "200", 100, 100},
		{"negative from end", "-10", 100, 90},
		{"negative beyond start", "-200", 100, 0},
		{"invalid", "abc", 100, 0},
		{"zero", "0", 100, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseLogOffset(tt.input, tt.fileSize)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReadWithLineBoundaries(t *testing.T) {
	f := tempFileWithContent(t, "hello world, this is a test\n")
	data, err := ReadWithLineBoundaries(f, 5)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestReadWithLineBoundaries_FullContent(t *testing.T) {
	content := "line1\nline2\n"
	f := tempFileWithContent(t, content)
	data, err := ReadWithLineBoundaries(f, 1024)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestReadLogIndex(t *testing.T) {
	dir := t.TempDir()
	idxPath := filepath.Join(dir, "test.log.idx")

	// Write 3 index entries as little-endian uint64
	f, err := os.Create(idxPath)
	require.NoError(t, err)
	for _, offset := range []uint64{0, 2048, 4096} {
		require.NoError(t, binary.Write(f, binary.LittleEndian, offset))
	}
	require.NoError(t, f.Close())

	indices, err := ReadLogIndex(idxPath)
	require.NoError(t, err)
	assert.Equal(t, []int64{0, 2048, 4096}, indices)
}

func TestReadLogIndex_MissingFile(t *testing.T) {
	_, err := ReadLogIndex("/nonexistent/path.idx")
	assert.Error(t, err)
}
