// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/logutil"
)

func writeLog(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")
	var buf strings.Builder
	for _, l := range lines {
		buf.WriteString(logutil.FormatLine(l, logutil.StreamStdout))
	}
	require.NoError(t, os.WriteFile(path, []byte(buf.String()), 0o600))
	return path
}

func TestOutputTail_NilOrMissing(t *testing.T) {
	tail := NewOutputTail()
	assert.Equal(t, "", tail("", 3, 300))

	// Non-existent file produces "" (not an error).
	assert.Equal(t, "", tail(filepath.Join(t.TempDir(), "missing.log"), 3, 300))
}

func TestOutputTail_ShortOutputUnchanged(t *testing.T) {
	path := writeLog(t, "first", "second", "third")
	tail := NewOutputTail()
	got := tail(path, 3, 300)
	assert.Equal(t, "first\nsecond\nthird", got)
}

func TestOutputTail_LimitsToLastNLines(t *testing.T) {
	path := writeLog(t, "a", "b", "c", "d", "e")
	tail := NewOutputTail()
	got := tail(path, 3, 300)
	assert.Equal(t, "c\nd\ne", got)
}

func TestOutputTail_SkipsEmptyLines(t *testing.T) {
	path := writeLog(t, "real", "", "    ", "tail")
	tail := NewOutputTail()
	got := tail(path, 4, 300)
	assert.Equal(t, "real\ntail", got)
}

func TestOutputTail_TruncatesToMaxBytesWithEllipsis(t *testing.T) {
	long := strings.Repeat("X", 200)
	path := writeLog(t, long, long)
	tail := NewOutputTail()
	got := tail(path, 3, 50)
	assert.True(t, strings.HasPrefix(got, "…"), "expected ellipsis prefix, got %q", got)
	// budget is 50-len("…")=47 (since "…" is 3 bytes in UTF-8)
	assert.Equal(t, 50, len(got))
}
