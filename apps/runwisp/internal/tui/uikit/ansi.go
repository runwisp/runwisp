// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func walkANSISegments(s string, fn func(seg string, printable bool)) {
	runes := []rune(s)
	n := len(runes)
	segStart := 0

	for i := 0; i < n; i++ {
		if runes[i] != '\x1b' {
			continue
		}

		// Flush printable segment that ends here.
		if i > segStart {
			fn(string(runes[segStart:i]), true)
		}

		seqStart := i
		i++ // consume ESC

		if i >= n {
			fn(string(runes[seqStart:i]), false)
			segStart = i
			break
		}

		i = skipEscapeSequence(runes, n, i)
		fn(string(runes[seqStart:i]), false)
		// The outer loop's i++ will move past the last consumed character,
		// so back up one to compensate.
		i--
		segStart = i + 1
	}

	if segStart < n {
		fn(string(runes[segStart:n]), true)
	}
}

// skipEscapeSequence advances past the escape sequence starting at pos (after ESC).
func skipEscapeSequence(runes []rune, n, pos int) int {
	switch runes[pos] {
	case '[': // CSI – ends at a byte in [0x40, 0x7e].
		return skipCSISequence(runes, n, pos+1)
	case ']': // OSC – ends at BEL or ST (ESC \).
		return skipOSCSequence(runes, n, pos+1)
	case 'P', '_', '^', 'X': // DCS, APC, PM, SOS – end at ST.
		return skipSTSequence(runes, n, pos+1)
	default: // Two-byte sequence, e.g. ESC M (RI), ESC c (RIS).
		return pos + 1
	}
}

func skipCSISequence(runes []rune, n, i int) int {
	for i < n && (runes[i] < 0x40 || runes[i] > 0x7e) {
		i++
	}
	if i < n {
		i++ // consume final byte
	}
	return i
}

func skipOSCSequence(runes []rune, n, i int) int {
	for i < n {
		if runes[i] == '\x07' {
			return i + 1
		}
		if runes[i] == '\x1b' && i+1 < n && runes[i+1] == '\\' {
			return i + 2
		}
		i++
	}
	return i
}

func skipSTSequence(runes []rune, n, i int) int {
	for i < n {
		if runes[i] == '\x1b' && i+1 < n && runes[i+1] == '\\' {
			return i + 2
		}
		i++
	}
	return i
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
	cs := &columnSlicer{start: start, cols: cols}
	walkANSISegments(s, cs.processSegment)
	return cs.buf.String(), cs.beyond
}

// columnSlicer accumulates the visible column slice of an ANSI string.
type columnSlicer struct {
	buf    strings.Builder
	col    int
	beyond bool
	done   bool
	start  int
	cols   int
}

func (cs *columnSlicer) processSegment(seg string, printable bool) {
	if cs.done {
		return
	}
	if !printable {
		// Include ANSI sequences before the visible window ends so colour
		// state set before the window is inherited correctly.
		if !cs.beyond {
			cs.buf.WriteString(seg)
		}
		return
	}
	for _, r := range seg {
		if cs.done {
			break
		}
		cs.processRune(r)
	}
}

func (cs *columnSlicer) processRune(r rune) {
	w := ansi.StringWidth(string(r))
	if w == 0 {
		// Zero-width characters tag along with the preceding visible character.
		if cs.col > cs.start && cs.col <= cs.start+cs.cols {
			cs.buf.WriteRune(r)
		}
		return
	}
	if cs.col+w > cs.start+cs.cols {
		cs.beyond = true
		cs.done = true
		return
	}
	if cs.col >= cs.start {
		cs.buf.WriteRune(r)
	}
	cs.col += w
}
