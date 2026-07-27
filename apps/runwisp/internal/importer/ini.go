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
// `key=value` (or `key:value`) pairs, full-line comments starting with `;` or
// `#`, and ConfigParser-style continuation lines (a line indented further than
// its key appends to the previous value). It is intentionally small — just
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
}

// feed classifies one raw line and folds it into the parser state.
func (p *iniParser) feed(raw string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		p.lastKey = ""
		return
	}
	// Continuation: indented line that isn't a new section/comment and we have a
	// key in flight.
	if p.isContinuation(raw, trimmed) {
		p.cur.values[p.lastKey] += "\n" + trimmed
		return
	}
	if trimmed[0] == ';' || trimmed[0] == '#' {
		p.lastKey = ""
		return
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		p.startSection(trimmed)
		return
	}
	if p.cur == nil {
		return // stray key before any section header
	}
	p.addKeyValue(trimmed)
}

func (p *iniParser) isContinuation(raw, trimmed string) bool {
	return (raw[0] == ' ' || raw[0] == '\t') && p.lastKey != "" && p.cur != nil &&
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
