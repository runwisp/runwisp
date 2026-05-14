// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package uikit

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// WalkANSISegments splits s into segments and calls fn for each one.
//
// fn receives the raw segment text and whether the segment is printable content
// (printable=true) or an ANSI/C1 escape sequence (printable=false). Escape
// sequences carry zero visual width; printable segments carry the visible
// characters that consume terminal columns.
//
// Supported sequences:
//   - CSI: ESC [ … final-byte  (covers all SGR colour codes)
//   - OSC: ESC ] … BEL or ESC ]  … ST
//   - DCS / APC / PM / SOS: ESC P/_ ^/X … ST
//   - Any other two-byte ESC + char sequence
func WalkANSISegments(s string, fn func(seg string, printable bool)) {
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

		switch runes[i] {
		case '[': // CSI – ends at a byte in [0x40, 0x7e].
			i++
			for i < n && (runes[i] < 0x40 || runes[i] > 0x7e) {
				i++
			}
			if i < n {
				i++ // consume final byte
			}

		case ']': // OSC – ends at BEL or ST (ESC \).
			i++
			for i < n {
				if runes[i] == '\x07' {
					i++
					break
				}
				if runes[i] == '\x1b' && i+1 < n && runes[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}

		case 'P', '_', '^', 'X': // DCS, APC, PM, SOS – end at ST.
			i++
			for i < n {
				if runes[i] == '\x1b' && i+1 < n && runes[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}

		default: // Two-byte sequence, e.g. ESC M (RI), ESC c (RIS).
			i++
		}

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

	var (
		buf    strings.Builder
		col    int  // current visual column (printable chars only)
		beyond bool // content extends past start+cols
		done   bool // stop processing further segments
	)

	WalkANSISegments(s, func(seg string, printable bool) {
		if done {
			return
		}
		if !printable {
			// Always include ANSI sequences up to the end of the visible window
			// so that colour state set before the window is inherited correctly.
			if !beyond {
				buf.WriteString(seg)
			}
			return
		}
		for _, r := range seg {
			if done {
				break
			}
			w := ansi.StringWidth(string(r))
			if w == 0 {
				// Zero-width characters (combining marks etc.) tag along with
				// the preceding visible character.
				if col > start && col <= start+cols {
					buf.WriteRune(r)
				}
				continue
			}
			if col+w > start+cols {
				beyond = true
				done = true
				break
			}
			if col >= start {
				buf.WriteRune(r)
			}
			col += w
		}
	})

	return buf.String(), beyond
}
