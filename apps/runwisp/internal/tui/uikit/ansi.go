// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"image/color"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// trailingEscapes matches a run of ANSI escape sequences anchored at the end of
// a string (CSI, OSC, and other Fe sequences). ansi.Cut keeps zero-width
// sequences that sit past the visible cut point; SliceLineColumns historically
// dropped everything past the cut, so on a clip we strip that dangling trailing
// style. It only ever removes zero-width bytes, so the visible slice is
// unchanged.
var trailingEscapes = regexp.MustCompile(`(?:\x1b\[[0-9;:?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_])+$`)

// VisibleWidth returns the display width of s in terminal cells, treating ANSI
// escape sequences as zero-width. Results are identical to ansi.StringWidth, but
// it fast-paths the hot case — ASCII text with SGR colour codes — instead of
// running the grapheme-cluster walk unconditionally (that walk dominates the TUI
// render loop). Any byte ≥ 0x80 (a possible wide/multibyte rune) or a non-CSI
// escape (OSC, hyperlinks) defers to the accurate path.
func VisibleWidth(s string) int {
	w, i, n := 0, 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c >= 0x80:
			return ansi.StringWidth(s)
		case c == 0x1b:
			if i+1 >= n || s[i+1] != '[' {
				return ansi.StringWidth(s)
			}
			i += 2
			for i < n && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			if i < n {
				i++ // step past the CSI final byte
			}
		case c >= 0x20 && c != 0x7f:
			w++
			i++
		default:
			i++ // other C0 control byte — zero width
		}
	}
	return w
}

// bgSGRCache memoises the opening SGR sequence per background colour. The Bubble
// Tea view loop is single-threaded, so no lock is needed.
var bgSGRCache = map[color.Color]string{}

// FillBg returns n spaces painted with bg, byte-for-byte identical to
// lipgloss.NewStyle().Background(bg).Render(spaces) but without re-deriving the
// SGR sequence and re-walking the string for width on every call — the single
// biggest cost in the scroll render path.
func FillBg(n int, bg color.Color) string {
	if n <= 0 {
		return ""
	}
	sgr, ok := bgSGRCache[bg]
	if !ok {
		sgr = ansi.NewStyle().BackgroundColor(bg).String()
		bgSGRCache[bg] = sgr
	}
	return sgr + strings.Repeat(" ", n) + "\x1b[m"
}

// TruncateToWidth truncates s to at most w visible columns, appending an
// ellipsis ("…") when it had to cut. ANSI escape sequences and wide runes are
// measured by display width, so a styled value that already fits is returned
// unchanged. Returns "" for w <= 0.
func TruncateToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// BaseSGR returns the opening SGR sequence for a foreground/background pair
// (optionally italic) — the pane's own colour state, used to re-assert it after
// an embedded reset (see ReassertResets).
func BaseSGR(fg, bg color.Color, italic bool) string {
	s := ansi.NewStyle().BackgroundColor(bg).ForegroundColor(fg)
	if italic {
		s = s.Italic(true)
	}
	return s.String()
}

// ReassertResets rewrites embedded SGR resets so a mid-line reset re-inherits
// base (the pane's own colours) instead of falling back to the terminal
// default. Captured process output routinely contains a mid-line "\x1b[0m",
// which otherwise clears the pane background/foreground for the rest of the row.
//
// ponytail: only bare \x1b[0m / \x1b[m are handled; combined resets like
// \x1b[0;32m aren't split — extend the replacer if a job emits them.
func ReassertResets(text, base string) string {
	if base == "" || !strings.Contains(text, "\x1b[") {
		return text
	}
	return strings.NewReplacer(
		"\x1b[0m", "\x1b[0m"+base,
		"\x1b[m", "\x1b[m"+base,
	).Replace(text)
}

// SliceLineColumns returns a substring of s starting at column offset `start`
// spanning at most `cols` visible columns. Tabs are expanded to 4 spaces.
// The second return value reports whether content extends beyond the visible window.
//
// The function is ANSI-aware: escape sequences are treated as zero-width and
// any colour-setting sequences that appear before the visible window are
// included in the result so that the visible slice renders with the correct
// inherited colour state.
func SliceLineColumns(s string, start, cols int) (string, bool) {
	if cols <= 0 {
		return "", false
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	clipped := ansi.StringWidth(s) > start+cols
	out := ansi.Cut(s, start, start+cols)
	if clipped {
		out = trailingEscapes.ReplaceAllString(out, "")
	}
	return out, clipped
}
