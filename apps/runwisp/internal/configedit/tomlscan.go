// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"strconv"
	"strings"
)

// This file is the one piece of TOML awareness the surgical editors share. They
// work on text rather than a parsed document — that is the whole point, since a
// parse/re-render round-trip would silently drop the operator's comments — but
// naive line matching is not safe: a `run = """ … """` body can contain a line
// that looks exactly like a table header. Every editor here walks lines through
// scanTOMLLines so a header inside a multi-line string is never mistaken for a
// real one.

// tomlLine is one physical line of a TOML document.
type tomlLine struct {
	// Text is the line's content including its trailing newline, if it had one.
	Text string
	// Start and End are the line's byte offsets in the document; End is just
	// past the trailing newline, so text[:End] ends at a line boundary.
	Start, End int
	// Code is true when the line begins outside any multi-line string, i.e. when
	// a table header or key at the start of this line is real TOML structure
	// rather than string content.
	Code bool
}

// scanTOMLLines splits a TOML document into lines, tagging each with whether it
// starts inside a multi-line ("""/”') string.
//
// It is deliberately a lexer, not a parser: it tracks single- and multi-line
// strings and comments well enough to know whether a line starts inside string
// content, and nothing more. The one known imprecision is a multi-line basic
// string whose content ends with a quote (`…"""" `), where TOML lets the extra
// quote belong to the body; the scanner closes at the first `"""`. Callers use
// this to decide whether to *touch* a line, so the conservative direction — see
// structure that isn't there — would at worst refuse an edit, never corrupt one.
func scanTOMLLines(text string) []tomlLine {
	var lines []tomlLine
	inMulti := false
	delim := ""
	for idx := 0; idx < len(text); {
		end := len(text)
		if nl := strings.IndexByte(text[idx:], '\n'); nl >= 0 {
			end = idx + nl + 1
		}
		line := text[idx:end]
		lines = append(lines, tomlLine{Text: line, Start: idx, End: end, Code: !inMulti})
		inMulti, delim = scanLineStrings(line, inMulti, delim)
		idx = end
	}
	return lines
}

// scanLineStrings advances the multi-line-string state across one line,
// returning whether the *next* line starts inside a multi-line string and which
// delimiter would close it.
func scanLineStrings(line string, inMulti bool, delim string) (bool, string) {
	i := 0
	for i < len(line) {
		if inMulti {
			close := strings.Index(line[i:], delim)
			if close < 0 {
				return true, delim
			}
			i += close + len(delim)
			inMulti, delim = false, ""
			continue
		}
		switch {
		case line[i] == '#':
			return false, "" // rest of the line is a comment
		case strings.HasPrefix(line[i:], `"""`):
			inMulti, delim = true, `"""`
			i += 3
		case strings.HasPrefix(line[i:], "'''"):
			inMulti, delim = true, "'''"
			i += 3
		case line[i] == '"':
			i += skipSingleLineString(line[i:], true)
		case line[i] == '\'':
			i += skipSingleLineString(line[i:], false)
		default:
			i++
		}
	}
	return inMulti, delim
}

// skipSingleLineString returns how many bytes of s the opening single-line
// string at s[0] consumes, including both quotes. An unterminated string
// consumes the rest of the line — TOML wouldn't parse anyway, and stopping here
// keeps the scanner from reading structure out of a broken value. basic=true
// honors backslash escapes; literal ('…') strings have none.
func skipSingleLineString(s string, basic bool) int {
	quote := s[0]
	for i := 1; i < len(s); i++ {
		if basic && s[i] == '\\' {
			i++
			continue
		}
		if s[i] == quote {
			return i + 1
		}
	}
	return len(s)
}

// tableHeader parses a table header line, allowing surrounding whitespace and a
// trailing comment: "[daemon]" yields ("daemon", false, true) and
// "[[notifier]]" yields ("notifier", true, true). ok is false for any other
// line. Callers must check tomlLine.Code first — this function looks at one
// line and cannot know it sits inside a string.
func tableHeader(line string) (name string, array, ok bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "[") {
		return "", false, false
	}
	array = strings.HasPrefix(t, "[[")
	open, close := "[", "]"
	if array {
		open, close = "[[", "]]"
	}
	rest := t[len(open):]
	end := strings.Index(rest, close)
	if end < 0 {
		return "", false, false
	}
	name = strings.TrimSpace(rest[:end])
	if name == "" {
		return "", false, false
	}
	after := strings.TrimSpace(rest[end+len(close):])
	if after != "" && !strings.HasPrefix(after, "#") {
		return "", false, false
	}
	return name, array, true
}

// splitDottedKey splits a table header's name into its segments, unquoting the
// quoted ones: `tasks.backup` and `tasks."backup"` both yield
// ["tasks", "backup"], and `tasks."my.job"` yields ["tasks", "my.job"] rather
// than treating the dot inside the quotes as a separator. ok is false for a
// malformed name (an unterminated quote, an empty segment, or trailing junk
// after a quoted segment) — callers refuse to touch a header they can't read
// rather than guess at its identity.
func splitDottedKey(name string) (segs []string, ok bool) {
	rest := strings.TrimSpace(name)
	for {
		seg, tail, ok := nextKeySegment(rest)
		if !ok {
			return nil, false
		}
		segs = append(segs, seg)
		if tail == "" {
			return segs, true
		}
		if tail[0] != '.' {
			return nil, false // junk between segments
		}
		if rest = strings.TrimSpace(tail[1:]); rest == "" {
			return nil, false // trailing dot
		}
	}
}

// nextKeySegment reads one segment off the front of a dotted key, returning it
// unquoted along with the unconsumed remainder (which still carries its leading
// dot, if any). ok is false for an empty or unterminated segment.
func nextKeySegment(s string) (seg, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if s[0] == '"' || s[0] == '\'' {
		n := skipSingleLineString(s, s[0] == '"')
		if n < 2 || s[n-1] != s[0] {
			return "", "", false // unterminated quote
		}
		if seg, ok = unquoteSegment(s[:n]); !ok {
			return "", "", false
		}
		return seg, strings.TrimSpace(s[n:]), seg != ""
	}
	if end := strings.IndexByte(s, '.'); end >= 0 {
		seg = strings.TrimSpace(s[:end])
		return seg, s[end:], seg != ""
	}
	seg = strings.TrimSpace(s)
	return seg, "", seg != ""
}

// unquoteSegment strips the surrounding quotes from one dotted-key segment. A
// literal ('…') key has no escapes, so its body is taken as-is; a basic ("…")
// key goes through strconv.Unquote, whose escape set matches TOML's for the
// characters a table name can hold. ok is false when the escapes don't decode —
// half-unescaping a name would make the caller edit the wrong table.
func unquoteSegment(s string) (string, bool) {
	if s[0] == '\'' {
		return s[1 : len(s)-1], true
	}
	body, err := strconv.Unquote(s)
	if err != nil {
		return "", false
	}
	return body, true
}
