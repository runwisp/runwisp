// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestSanitizeControls(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text untouched", "hello world", "hello world"},
		{"utf8 preserved", "café — ☕", "café — ☕"},
		{"tab kept", "a\tb", "a\tb"},
		{"sgr color kept", "\x1b[31mred\x1b[0m", "\x1b[31mred\x1b[0m"},
		{"sgr with params kept", "\x1b[1;38;5;208mx\x1b[m", "\x1b[1;38;5;208mx\x1b[m"},
		// OSC-8 hyperlink: label survives, the escape wrapper is stripped.
		{"osc8 hyperlink stripped (BEL)", "\x1b]8;;https://evil\x07click\x1b]8;;\x07", "click"},
		{"osc8 hyperlink stripped (ST)", "\x1b]8;;https://evil\x1b\\click\x1b]8;;\x1b\\", "click"},
		{"window title stripped", "\x1b]0;pwned\x07safe", "safe"},
		{"osc52 clipboard stripped", "\x1b]52;c;ZXZpbA==\x07x", "x"},
		{"screen erase stripped", "before\x1b[2Jafter", "beforeafter"},
		{"cursor move stripped", "\x1b[10;5Hhi", "hi"},
		{"dcs stripped", "\x1bPq#0;2\x1b\\text", "text"},
		{"apc stripped", "\x1b_Gfoo\x1b\\bar", "bar"},
		{"stray C0 dropped", "a\x00\x07\x08b", "ab"},
		{"lone esc dropped", "x\x1b", "x"},
		{"unterminated osc dropped to end", "keep\x1b]0;tail", "keep"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SanitizeControls(tc.in))
		})
	}
}

func TestReassertResets(t *testing.T) {
	base := BaseSGR(ColorText, ColorBg, false)
	assert.NotEmpty(t, base)

	// A mid-line reset (as captured process output emits) is followed by the
	// base SGR so the pane colours are re-applied for the rest of the row.
	in := "\x1b[1m[backup]\x1b[0m starting snapshot"
	out := ReassertResets(in, base)
	assert.Equal(t, "\x1b[1m[backup]\x1b[0m"+base+" starting snapshot", out)

	// Bare \x1b[m form is handled too.
	assert.Equal(t, "x\x1b[m"+base+"y", ReassertResets("x\x1b[my", base))

	// No escape sequences → returned unchanged (fast path).
	assert.Equal(t, "plain text", ReassertResets("plain text", base))

	// Empty base is a no-op.
	assert.Equal(t, in, ReassertResets(in, ""))

	// Every embedded reset is patched.
	got := ReassertResets("\x1b[0ma\x1b[0mb", base)
	assert.Equal(t, 2, strings.Count(got, base))
}

func TestOverlayAt_SplicesBoxKeepingSurroundings(t *testing.T) {
	base := "\x1b[31mabcdefghij\x1b[0m\n0123456789\nKLMNOPQRST"
	got := OverlayAt(base, "XX\nYY", 3, 1)
	lines := strings.Split(got, "\n")
	assert.Equal(t, "\x1b[31mabcdefghij\x1b[0m", lines[0], "rows above the box are untouched")
	assert.Equal(t, "012XX56789", ansi.Strip(lines[1]))
	assert.Equal(t, "KLMYYPQRST", ansi.Strip(lines[2]))
	for _, ln := range lines {
		assert.Equal(t, 10, ansi.StringWidth(ln))
	}
}

func TestOverlayAt_DropsRowsPastBaseAndPadsShortLines(t *testing.T) {
	got := OverlayAt("ab\ncd", "XX\nYY\nZZ", 4, 1)
	lines := strings.Split(got, "\n")
	assert.Len(t, lines, 2)
	assert.Equal(t, "cd  XX", ansi.Strip(lines[1]))
}

// Truncating an ANSI-styled line cuts by display column, never through an
// escape sequence.
func TestTruncateToWidth_ANSIAware(t *testing.T) {
	styled := "\x1b[31m" + strings.Repeat("x", 40) + "\x1b[0m"
	out := TruncateToWidth(styled, 10)
	assert.Equal(t, 10, VisibleWidth(out))
	assert.Equal(t, strings.Repeat("x", 9)+"…", ansi.Strip(out))
}
