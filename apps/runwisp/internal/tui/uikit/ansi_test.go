// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"strings"
	"testing"

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
