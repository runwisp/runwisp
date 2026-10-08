// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"bufio"
	"io"
	"strings"
)

// iniSection is one [header] block of an INI file. keys preserves first-seen
// order of its keys so callers can iterate deterministically; values holds the
// looked-up values.
type iniSection struct {
	name   string
	keys   []string
	values map[string]string
}

func (s *iniSection) set(key, value string) {
	if _, ok := s.values[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.values[key] = value
}

// get returns the value and whether the key was present.
func (s *iniSection) get(key string) (string, bool) {
	v, ok := s.values[key]
	return v, ok
}

// parseINI parses the supervisord dialect of INI: `[section]` headers,
// `key=value` (or `key:value`) pairs, `;`/`#` comments (full-line, or inline
// after whitespace), and ConfigParser-style continuation lines (a line
// indented further than its key appends to the previous value). It is intentionally small — just
// enough to read supervisord configs, not a general INI library.
func parseINI(r io.Reader) ([]iniSection, error) {
	p := &iniParser{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		p.feed(sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return p.sections, nil
}

// iniParser holds the running state of a parseINI scan: the sections built so
// far, the current section, and the last key seen (for continuation lines).
type iniParser struct {
	sections []iniSection
	cur      *iniSection
	lastKey  string
	indent   int // leading whitespace of lastKey's line; a continuation is indented further
	blanks   int // blank lines since lastKey's value; kept if the value goes on
}

// feed classifies one raw line and folds it into the parser state.
func (p *iniParser) feed(raw string) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
		return // like ConfigParser, a comment line doesn't end a multi-line value
	}
	raw = stripInlineComment(raw)
	trimmed = strings.TrimSpace(raw)
	if trimmed == "" {
		p.blanks++ // like ConfigParser, a blank line alone doesn't end a value
		return
	}
	// Continuation: a line indented further than the key in flight that isn't a
	// new section. Keys indented alike are separate keys, as in ConfigParser.
	indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
	if p.isContinuation(indent, trimmed) {
		p.cur.values[p.lastKey] += strings.Repeat("\n", p.blanks+1) + trimmed
		p.blanks = 0
		return
	}
	p.blanks = 0
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		p.startSection(trimmed)
		return
	}
	if p.cur == nil {
		return // stray key before any section header
	}
	p.addKeyValue(trimmed)
	p.indent = indent
}

// stripInlineComment drops a trailing `;` or `#` comment the way supervisord's
// ConfigParser does: the marker only starts a comment at the start of the line
// or after whitespace, so `a;b` and `a#b` keep their text.
func stripInlineComment(line string) string {
	for i := 0; i < len(line); i++ {
		if (line[i] == ';' || line[i] == '#') && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

func (p *iniParser) isContinuation(indent int, trimmed string) bool {
	return indent > p.indent && p.lastKey != "" && p.cur != nil &&
		!strings.HasPrefix(trimmed, "[")
}

func (p *iniParser) startSection(trimmed string) {
	name := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	p.sections = append(p.sections, iniSection{name: name, values: map[string]string{}})
	p.cur = &p.sections[len(p.sections)-1]
	p.lastKey = ""
}

func (p *iniParser) addKeyValue(trimmed string) {
	sep := strings.IndexAny(trimmed, "=:")
	if sep < 0 {
		p.lastKey = ""
		return
	}
	key := strings.TrimSpace(trimmed[:sep])
	value := strings.TrimSpace(trimmed[sep+1:])
	if key == "" {
		return
	}
	p.cur.set(key, value)
	p.lastKey = key
}
