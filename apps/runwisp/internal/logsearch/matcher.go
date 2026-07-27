// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logsearch performs on-demand substring or regex search across the
// on-disk log files of a task's runs. There is no in-memory index, no
// background goroutine, and no state survives a request.
package logsearch

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Matcher decides whether a single log line matches the user's query.
// Implementations must be safe to call from a single goroutine for the
// duration of one scan (each ScanRun call holds its own matcher state).
type Matcher interface {
	Match(line []byte) bool
}

// NewMatcher returns the matcher implied by the user-supplied query.
// regex=true switches to RE2; caseSensitive controls case folding for the
// substring path. Returns an error suitable for huma to map to 400 when the
// query is empty or a regex fails to compile.
func NewMatcher(q string, regex, caseSensitive bool) (Matcher, error) {
	if q == "" {
		return nil, fmt.Errorf("logsearch: empty query")
	}
	if regex {
		pat := q
		if !caseSensitive {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("logsearch: invalid regex: %w", err)
		}
		return &Regexp{re: re}, nil
	}
	if caseSensitive {
		return &Substring{needle: []byte(q)}, nil
	}
	return &Substring{
		needle:          []byte(strings.ToLower(q)),
		caseFold:        true,
		caseFoldScratch: make([]byte, 0, 256),
	}, nil
}

// Substring matches by case-(in)sensitive byte substring. The
// case-insensitive path reuses one lowercase scratch buffer for the haystack
// across every line of a scan, so steady-state allocation is one buffer per
// scan goroutine regardless of line count.
type Substring struct {
	needle          []byte
	caseFold        bool
	caseFoldScratch []byte
}

func (s *Substring) Match(line []byte) bool {
	if !s.caseFold {
		return bytes.Contains(line, s.needle)
	}
	if cap(s.caseFoldScratch) < len(line) {
		s.caseFoldScratch = make([]byte, len(line))
	} else {
		s.caseFoldScratch = s.caseFoldScratch[:len(line)]
	}
	// ASCII fast path. Falls through to unicode for non-ASCII bytes — keeps
	// case-folding correct for UTF-8 input without paying the unicode cost
	// when logs are pure ASCII (the common case).
	for i, b := range line {
		if b < utf8MaxASCII {
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			s.caseFoldScratch[i] = b
			continue
		}
		// Unicode-aware fallback: drop to per-rune ToLower for the whole
		// remainder so we don't mis-fold multi-byte sequences split across
		// the loop boundary. Folding can grow the byte length — invalid
		// UTF-8 bytes decode to U+FFFD (3 bytes each) — so the lowered
		// remainder may exceed the scratch cap (sized to len(line)). Grow
		// the buffer to the exact need before copying to avoid a slice-bounds
		// panic on long non-ASCII lines.
		lowered := strings.Map(unicode.ToLower, string(line[i:]))
		need := i + len(lowered)
		if cap(s.caseFoldScratch) < need {
			grown := make([]byte, need)
			copy(grown, s.caseFoldScratch[:i])
			s.caseFoldScratch = grown
		} else {
			s.caseFoldScratch = s.caseFoldScratch[:need]
		}
		copy(s.caseFoldScratch[i:], lowered)
		break
	}
	return bytes.Contains(s.caseFoldScratch, s.needle)
}

// utf8MaxASCII is one past the highest single-byte ASCII codepoint. Anything
// at or above this byte is part of a multi-byte UTF-8 sequence that the
// ASCII case-fold cannot safely touch in place.
const utf8MaxASCII = 0x80

// Regexp matches by precompiled RE2. The regex compile happens once before
// the scan begins, so per-line work is bounded by RE2's linear-time match.
type Regexp struct {
	re *regexp.Regexp
}

func (r *Regexp) Match(line []byte) bool { return r.re.Match(line) }
