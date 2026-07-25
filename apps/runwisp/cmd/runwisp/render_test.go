// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTerminalWidthWithoutATerminalIsZero is the load-bearing case for every
// `assert.Contains` in this package: a bytes.Buffer has no width, so nothing
// wraps and the assertions hold by construction rather than by luck. It's also
// what keeps `runwisp import cron 2>&1 | grep '2>&1'` finding its line.
func TestTerminalWidthWithoutATerminalIsZero(t *testing.T) {
	t.Setenv("COLUMNS", "")
	assert.Equal(t, 0, terminalWidth(&bytes.Buffer{}))

	// A real file has an Fd but is not a terminal, which term.GetSize reports as
	// an error — that error *is* the answer, so there's no isatty check.
	f, err := os.CreateTemp(t.TempDir(), "width")
	require.NoError(t, err)
	defer f.Close()
	assert.Equal(t, 0, terminalWidth(f))
}

// TestTerminalWidthHonorsColumns covers the escape hatch for pinning a width
// through a pipe, and the values that must fall through to detection rather than
// being read as a width of zero.
func TestTerminalWidthHonorsColumns(t *testing.T) {
	cases := map[string]int{
		"60":   60,
		" 72 ": 72,
		"200":  maxRenderWidth, // clamped: prose stops being readable past this
		"39":   0,              // below minRenderWidth, wrapping shreds more than it helps
		"abc":  0,              // not a number → fall through (a buffer has no width)
		"0":    0,
		"-5":   0,
		"":     0,
	}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv("COLUMNS", value)
			assert.Equal(t, want, terminalWidth(&bytes.Buffer{}))
		})
	}
}

// TestWrapAfterRespectsTheLimitAndIndents pins the two things the layout depends
// on: no line exceeds the width, and continuations sit at the hanging indent.
func TestWrapAfterRespectsTheLimitAndIndents(t *testing.T) {
	text := strings.Repeat("alpha beta gamma ", 8)
	lines := wrapAfter(text, 40, 6, 8)

	require.Greater(t, len(lines), 1)
	for _, line := range lines {
		assert.LessOrEqual(t, ansi.StringWidth(line), 40, "%q", line)
	}
	assert.True(t, strings.HasPrefix(lines[0], strings.Repeat(" ", 6)))
	for _, line := range lines[1:] {
		assert.True(t, strings.HasPrefix(line, strings.Repeat(" ", 8)), "%q", line)
	}
}

// TestWrapAfterBreaksAnOverlongToken is why this isn't plain word wrapping: a
// single unbreakable token has to be broken anyway, or it overflows the terminal
// and takes the layout with it.
func TestWrapAfterBreaksAnOverlongToken(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("x", 200)
	lines := wrapAfter(url, 40, 6, 8)

	require.Greater(t, len(lines), 4)
	for _, line := range lines {
		assert.LessOrEqual(t, ansi.StringWidth(line), 40, "%q", line)
	}
	var rebuilt strings.Builder
	for _, line := range lines {
		rebuilt.WriteString(strings.TrimSpace(line))
	}
	assert.Equal(t, url, rebuilt.String(), "breaking must not lose or add characters")
}

// TestWrapAfterKeepsFlagsIntact is the reason wrapping isn't ansi.Wrap: that
// treats every hyphen as a breakpoint, so `--exclude` becomes `--` / `exclude`
// and the operator reads a command that isn't the one that will run.
func TestWrapAfterKeepsFlagsIntact(t *testing.T) {
	text := "/usr/bin/find /tmp " + strings.Repeat("--exclude /tmp/keep ", 6)
	for _, line := range wrapAfter(text, 40, 6, 8) {
		trimmed := strings.TrimSpace(line)
		assert.False(t, trimmed == "--" || strings.HasSuffix(trimmed, " --"),
			"a flag was split at its hyphen: %q", line)
	}
}

// TestWrapAfterWithoutAWidthDoesNotReflow pins the opinionated part: guessing 80
// columns for a pipe is how a redirect tail goes missing from a grep.
func TestWrapAfterWithoutAWidthDoesNotReflow(t *testing.T) {
	text := strings.Repeat("alpha beta ", 30)
	lines := wrapAfter(text, 0, 6, 8)
	require.Len(t, lines, 1)
	assert.Equal(t, strings.Repeat(" ", 6)+text, lines[0])
}

// TestPadToMeasuresInCells is the same bytes-vs-runes-vs-cells trap the layout
// column depends on getting right.
func TestPadToMeasuresInCells(t *testing.T) {
	assert.Equal(t, 8, ansi.StringWidth(padTo("abc", 8)))
	assert.Equal(t, 8, ansi.StringWidth(padTo("备份", 8)), "two wide glyphs are 4 cells")
	assert.Equal(t, "abcdef", padTo("abcdef", 4), "already past the width, left alone")
}

// TestRendererForANonTerminalRendersPlain is the redirect case:
// `runwisp import cron 2> report.txt` must not collect escape sequences.
func TestRendererForANonTerminalRendersPlain(t *testing.T) {
	st := newImportStyles(&bytes.Buffer{})
	for _, got := range []string{
		st.ok.Render("✓"), st.changed.Render("~"), st.attn.Render("!"), st.dim.Render("-"),
	} {
		assert.Equal(t, ansi.Strip(got), got, "styled a non-terminal destination: %q", got)
	}
}
