// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import (
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

func TestSidecarIndex_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	f, err := os.Create(MetaPath(logPath))
	require.NoError(t, err)
	for _, offset := range []int64{0, 2048, 4096} {
		_, err := f.Write(IndexRecord(offset))
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())

	assert.Equal(t, []int64{0, 2048, 4096}, ReadSidecar(logPath).Index)
}

func TestSidecarIndex_MissingContainer(t *testing.T) {
	assert.Empty(t, ReadSidecar(filepath.Join(t.TempDir(), "absent.log")).Index)
}

// --- FormatLine ---

func TestFormatLine_Stdout(t *testing.T) {
	got := FormatLine("hello", StreamStdout)
	assert.Equal(t, "hello\n", got)
}

func TestFormatLine_Stderr(t *testing.T) {
	got := FormatLine("oops", StreamStderr)
	assert.Equal(t, "[ERR] oops\n", got)
}

func TestFormatLine_SystemStream(t *testing.T) {
	got := FormatLine("starting", StreamSystem)
	assert.Equal(t, "[SYSTEM] starting\n", got)
}

func TestFormatLine_CustomStream(t *testing.T) {
	got := FormatLine("msg", "trace")
	assert.Equal(t, "[TRACE] msg\n", got)
}

func TestFormatLine_StripsTrailingNewline(t *testing.T) {
	got := FormatLine("line\n", StreamStdout)
	assert.Equal(t, "line\n", got, "trailing newline must be stripped and re-added exactly once")
}

// --- ParseStreamPrefix ---

func TestParseStreamPrefix_Stdout(t *testing.T) {
	stream, text := ParseStreamPrefix("plain text")
	assert.Equal(t, StreamStdout, stream)
	assert.Equal(t, "plain text", text)
}

func TestParseStreamPrefix_Stderr(t *testing.T) {
	stream, text := ParseStreamPrefix("[ERR] error message")
	assert.Equal(t, StreamStderr, stream)
	assert.Equal(t, "error message", text)
}

func TestParseStreamPrefix_System(t *testing.T) {
	stream, text := ParseStreamPrefix("[SYSTEM] system message")
	assert.Equal(t, StreamSystem, stream)
	assert.Equal(t, "system message", text)
}

func TestParseStreamPrefix_SystemWithTimestamp(t *testing.T) {
	// "[2024-01-15T12:00:00] [SYSTEM] foo" — timestamp prefix is tolerated
	stream, text := ParseStreamPrefix("[2024-01-15T12:00:00] [SYSTEM] foo")
	assert.Equal(t, StreamSystem, stream)
	assert.Equal(t, "foo", text)
}

func TestParseStreamPrefix_StripTrailingNewline(t *testing.T) {
	stream, text := ParseStreamPrefix("output line\n")
	assert.Equal(t, StreamStdout, stream)
	assert.Equal(t, "output line", text)
}

func TestParseStreamPrefix_EmptyLine(t *testing.T) {
	stream, text := ParseStreamPrefix("")
	assert.Equal(t, StreamStdout, stream)
	assert.Equal(t, "", text)
}

// --- stripSystemPrefix ---

func TestStripSystemPrefix_ExactSystem(t *testing.T) {
	rest, ok := stripSystemPrefix("[SYSTEM] payload")
	assert.True(t, ok)
	assert.Equal(t, "payload", rest)
}

func TestStripSystemPrefix_NoMatch(t *testing.T) {
	_, ok := stripSystemPrefix("plain text")
	assert.False(t, ok)
}

func TestStripSystemPrefix_TimestampThenSystem(t *testing.T) {
	// "[2024-01-15T12:00:00.000Z] [SYSTEM] message"
	rest, ok := stripSystemPrefix("[2024-01-15T12:00:00.000Z] [SYSTEM] message")
	assert.True(t, ok)
	assert.Equal(t, "message", rest)
}

func TestStripSystemPrefix_ShortBracketedPrefix(t *testing.T) {
	// "[ERR] [SYSTEM] msg" — "[ERR]" is length 3 which is < 19 chars, so treated as a tag,
	// not a timestamp prefix. The CutPrefix then looks for [SYSTEM] on the remaining.
	_, ok := stripSystemPrefix("[ERR] [SYSTEM] msg")
	// "[ERR] " is head="ERR", len 3, < 19, so not stripped as timestamp.
	// rest stays "[ERR] [SYSTEM] msg", then CutPrefix("[SYSTEM] ") fails → false
	assert.False(t, ok)
}

func TestStripSystemPrefix_EmptyString(t *testing.T) {
	_, ok := stripSystemPrefix("")
	assert.False(t, ok)
}

// --- CalculateLineOffset with indices ---

func TestCalculateLineOffset_WithIndex_FirstChunk(t *testing.T) {
	// 3 lines of "abc\n" each (4 bytes), index=[0, 8] (every 2 lines)
	content := "abc\nabc\nabc\n"
	f := tempFileWithContent(t, content)
	indices := []int64{0, 8}
	// Requesting local line 1 (chunk 0, skip 1) → offset 4
	offset := CalculateLineOffset(f, indices, 1, LogMeta{})
	assert.Equal(t, int64(4), offset)
}

func TestCalculateLineOffset_WithIndex_SecondChunk(t *testing.T) {
	content := "abc\nabc\nabc\nabc\n"
	f := tempFileWithContent(t, content)
	// Each "abc\n" = 4 bytes; index entries every LogIndexInterval lines.
	// Use a small manual index to force the second chunk path.
	indices := []int64{0, 8}
	// Line 2 → chunkIdx=1 (8/2=4 exceeds but is clamped at len-1=1), base=8, skip=0 → offset 8
	meta := LogMeta{}
	offset := CalculateLineOffset(f, indices, 2, meta)
	assert.Equal(t, int64(8), offset)
}

func TestCalculateLineOffset_ChunkIdxClamped(t *testing.T) {
	content := "x\ny\nz\n"
	f := tempFileWithContent(t, content)
	// indices has only 1 entry; requesting a line beyond that clamps to last chunk
	indices := []int64{0}
	offset := CalculateLineOffset(f, indices, 100, LogMeta{})
	// clamped to last index (0), then skip 100 lines from 0 → EOF, returns end of file
	assert.True(t, offset >= 0)
}

// --- CalculateLineOffset edge case: RotatedLines exact ---

func TestCalculateLineOffset_RotatedLinesExact(t *testing.T) {
	// Line requested == RotatedLines → localLine 0, starts at offset 0
	f := tempFileWithContent(t, "line0\nline1\n")
	meta := LogMeta{RotatedLines: 3}
	offset := CalculateLineOffset(f, nil, 3, meta)
	assert.Equal(t, int64(0), offset)
}
