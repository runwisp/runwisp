// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
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
