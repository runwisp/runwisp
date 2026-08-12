// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestVisibleWidth_MatchesAnsi asserts the fast path returns exactly what the
// grapheme-accurate reference does across plain, styled, wide and mixed input.
func TestVisibleWidth_MatchesAnsi(t *testing.T) {
	cases := []string{
		"",
		"backup-postgres",
		"1.2s",
		"\x1b[38;2;1;2;3mcolored\x1b[m",
		lipgloss.NewStyle().Background(ColorPrimaryDim).Foreground(ColorWhite).Render("  running  "),
		"tab\tafter",
		"ellipsis…",    // wide-ish rune fallback
		"│ scrollbar",  // box-drawing → fallback
		"emoji 🚀 tail", // multibyte → fallback
		"\x1b[1;38;5;42mx\x1b[0my",
		strings.Repeat("a", 40),
	}
	for _, s := range cases {
		if got, want := VisibleWidth(s), ansi.StringWidth(s); got != want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", s, got, want)
		}
	}
}

// TestFillBg_MatchesLipgloss asserts the cached-SGR fill is byte-identical to the
// lipgloss render it replaces, so the change is purely a speedup.
func TestFillBg_MatchesLipgloss(t *testing.T) {
	for _, n := range []int{0, 1, 5, 20} {
		got := FillBg(n, ColorBg)
		want := ""
		if n > 0 {
			want = lipgloss.NewStyle().Background(ColorBg).Render(strings.Repeat(" ", n))
		}
		if got != want {
			t.Errorf("FillBg(%d) = %q, want %q", n, got, want)
		}
	}
}
