// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// Terminal-shaped rendering helpers: how wide the destination is, how to wrap
// text to it, and how to style for it. These live in package main rather than
// internal/textutil because that package's doc comment promises it stays
// dependency-free and this needs x/ansi to be correct about escape sequences
// and wide characters.

// maxRenderWidth caps how wide a wrapped block gets on a very wide terminal.
// Beyond about this, prose stops being readable in one eye movement.
const maxRenderWidth = 100

// minRenderWidth is the narrowest terminal worth reflowing for. Below it,
// wrapping produces more shredded lines than it saves, so we leave the text
// alone and let the terminal do whatever it does.
const minRenderWidth = 40

// terminalWidth reports how many cells wide w is, or 0 when that isn't a
// meaningful question — a file, a pipe, or a bytes.Buffer.
//
// Zero means "don't wrap", and that is the opinionated part: reflowing to a
// guessed 80 columns is exactly how `runwisp import cron 2>&1 | grep '2>&1'`
// loses the line the operator was looking for. $COLUMNS stays the escape hatch
// for pinning a width through a pipe.
//
// The width comes from the destination, not from os.Stdout: these summaries go
// to stderr, and the two are routinely pointed at different places.
func terminalWidth(w io.Writer) int {
	if cols, ok := columnsFromEnv(); ok {
		return clampRenderWidth(cols)
	}
	fder, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return 0
	}
	// An error here *is* the not-a-terminal answer, which is why there's no
	// separate isatty check.
	cols, _, err := term.GetSize(int(fder.Fd()))
	if err != nil {
		return 0
	}
	return clampRenderWidth(cols)
}

// columnsFromEnv reads $COLUMNS, the operator's override. A value that isn't a
// positive integer is ignored rather than treated as zero, so a stray
// `COLUMNS=abc` in an environment falls through to detection.
func columnsFromEnv() (int, bool) {
	raw := strings.TrimSpace(os.Getenv("COLUMNS"))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// clampRenderWidth keeps a detected width inside the band worth wrapping to,
// collapsing anything too narrow to 0 (don't wrap).
func clampRenderWidth(cols int) int {
	switch {
	case cols < minRenderWidth:
		return 0
	case cols > maxRenderWidth:
		return maxRenderWidth
	default:
		return cols
	}
}

// wrapAfter wraps plain text into lines that fit a terminal `width` cells wide,
// where the first line starts at column firstCol and every continuation is
// indented to `hanging`. The returned lines carry their own indentation, and
// none of them exceeds width cells.
//
// width 0 means don't wrap: the text comes back as its own lines (split on any
// newlines it already contains), indented but never reflowed.
//
// text must be plain — style short atomic tokens like a mark or a schedule
// instead, never a wrapped body, or a line break can land inside an escape
// sequence.
func wrapAfter(text string, width, firstCol, hanging int) []string {
	// Tabs make column arithmetic fiction, and a `run` script legitimately
	// contains them.
	text = strings.ReplaceAll(text, "\t", "    ")

	if width <= 0 {
		return indentEach(strings.Split(text, "\n"), firstCol, hanging)
	}

	// Wrap to whatever room is left after the deepest indent either line kind
	// will carry, so a continuation can't overflow.
	indent := firstCol
	if hanging > indent {
		indent = hanging
	}
	limit := width - indent
	if limit < 1 {
		limit = 1
	}
	return indentEach(wrapToLimit(text, limit), firstCol, hanging)
}

// wrapToLimit breaks text at whitespace only, hard-breaking a token that can't
// fit on a line of its own.
//
// x/ansi can't do this for us: both ansi.Wrap and ansi.Wordwrap treat a hyphen
// as an unconditional breakpoint, which turns `--exclude /tmp/keep` into `--` /
// `exclude /tmp/keep`. A wrapped shell command that reads as a *different* shell
// command is worse than a long line, and the text here is mostly shell commands.
// Measuring and the hard break stay x/ansi's job, since both have to be
// grapheme-aware and ANSI-safe.
func wrapToLimit(text string, limit int) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, wrapLineToLimit(line, limit)...)
	}
	return out
}

// wrapLineToLimit wraps a single line. A line that already fits is returned
// untouched, spacing and all — only a line that must be reflowed gets its runs
// of whitespace collapsed.
func wrapLineToLimit(line string, limit int) []string {
	if ansi.StringWidth(line) <= limit {
		return []string{line}
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		out = append(out, cur.String())
		cur.Reset()
	}
	for _, word := range strings.Fields(line) {
		width := ansi.StringWidth(word)
		if cur.Len() > 0 {
			if ansi.StringWidth(cur.String())+1+width <= limit {
				cur.WriteString(" ")
				cur.WriteString(word)
				continue
			}
			flush()
		}
		if width <= limit {
			cur.WriteString(word)
			continue
		}
		// A single token too long for any line — a 300-character URL, say. Break it
		// by cell rather than letting it overflow, and keep the remainder available
		// for the words that follow.
		parts := strings.Split(ansi.Hardwrap(word, limit, true), "\n")
		out = append(out, parts[:len(parts)-1]...)
		cur.WriteString(parts[len(parts)-1])
	}
	if cur.Len() > 0 {
		flush()
	}
	return out
}

// indentEach prefixes the first line with firstCol spaces and the rest with
// hanging spaces. Empty lines stay empty rather than becoming trailing
// whitespace.
func indentEach(lines []string, firstCol, hanging int) []string {
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if line == "" {
			out = append(out, "")
			continue
		}
		pad := hanging
		if i == 0 {
			pad = firstCol
		}
		out = append(out, strings.Repeat(" ", pad)+line)
	}
	return out
}

// padTo right-pads s to width display cells, measuring in cells rather than
// bytes or runes so a CJK name or an emoji doesn't shift the column after it.
// A string already at or past width is returned unchanged.
func padTo(s string, width int) string {
	if gap := width - ansi.StringWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}
