// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLineBuffer_CompleteLine(t *testing.T) {
	var lines []string
	lb := NewLineBuffer(func(line string) { lines = append(lines, line) })

	lb.Write([]byte("hello\n"))
	assert.Equal(t, []string{"hello\n"}, lines)
}

func TestLineBuffer_MultipleLines(t *testing.T) {
	var lines []string
	lb := NewLineBuffer(func(line string) { lines = append(lines, line) })

	lb.Write([]byte("line1\nline2\nline3\n"))
	assert.Equal(t, []string{"line1\n", "line2\n", "line3\n"}, lines)
}

func TestLineBuffer_SplitAcrossWrites(t *testing.T) {
	var lines []string
	lb := NewLineBuffer(func(line string) { lines = append(lines, line) })

	lb.Write([]byte("hel"))
	assert.Empty(t, lines)

	lb.Write([]byte("lo\n"))
	assert.Equal(t, []string{"hello\n"}, lines)
}

func TestLineBuffer_FlushPartialLine(t *testing.T) {
	var lines []string
	lb := NewLineBuffer(func(line string) { lines = append(lines, line) })

	lb.Write([]byte("partial"))
	assert.Empty(t, lines)

	lb.Flush()
	assert.Equal(t, []string{"partial\n"}, lines)
}

func TestLineBuffer_FlushEmpty(t *testing.T) {
	var lines []string
	lb := NewLineBuffer(func(line string) { lines = append(lines, line) })

	lb.Flush()
	assert.Empty(t, lines)
}

func TestLineBuffer_OverflowFlush(t *testing.T) {
	var lines []string
	lb := NewLineBuffer(func(line string) { lines = append(lines, line) })

	// Write data exceeding MaxLineBufferSize without a newline
	big := strings.Repeat("x", MaxLineBufferSize+1)
	lb.Write([]byte(big))
	assert.Len(t, lines, 1)
	assert.Equal(t, big, lines[0])
}

func TestLineBuffer_MixedCompleteAndPartial(t *testing.T) {
	var lines []string
	lb := NewLineBuffer(func(line string) { lines = append(lines, line) })

	lb.Write([]byte("first\nsec"))
	assert.Equal(t, []string{"first\n"}, lines)

	lb.Write([]byte("ond\nthird"))
	assert.Equal(t, []string{"first\n", "second\n"}, lines)

	lb.Flush()
	assert.Equal(t, []string{"first\n", "second\n", "third\n"}, lines)
}
