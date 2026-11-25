// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logsearch

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Term is one compiled log:grep term: the literal parts between `*`
// wildcards, in order, and whether its ends must sit on word edges. A `*`
// matches any run of characters on the line, including none.
type Term struct {
	Parts     []string
	WordStart bool
	WordEnd   bool
}

// Terms matches a line that matches every term. The station compiles the
// terms; with caseSensitive false their parts arrive lowercased
// (strings.ToLower) and Match lowercases the line the same way.
type Terms struct {
	terms []Term
	fold  bool
}

// NewTerms validates terms: at least one, each with at least one part, and
// no empty part.
func NewTerms(terms []Term, caseSensitive bool) (*Terms, error) {
	if len(terms) == 0 {
		return nil, errors.New("logsearch: no terms")
	}
	for _, t := range terms {
		if len(t.Parts) == 0 {
			return nil, errors.New("logsearch: a term has no parts")
		}
		for _, p := range t.Parts {
			if p == "" {
				return nil, errors.New("logsearch: a term has an empty part")
			}
		}
	}
	return &Terms{terms: terms, fold: !caseSensitive}, nil
}

// Match reports whether line contains every term. Pass the visible text
// (VisibleText), the same text log:search matches.
func (t *Terms) Match(line string) bool {
	if t.fold {
		line = strings.ToLower(line)
	}
	for _, term := range t.terms {
		if !term.match(line) {
			return false
		}
	}
	return true
}

// match finds the term in s. The first part takes its leftmost occurrence
// that satisfies WordStart and the middle parts their leftmost ones after it:
// matching as early as possible leaves the most room for what follows, so
// only the last part, which carries WordEnd, has to try every occurrence.
func (t Term) match(s string) bool {
	parts := t.Parts
	first, last := parts[0], parts[len(parts)-1]
	startOK := func(i int) bool { return !t.WordStart || wordStartAt(s, i) }
	endOK := func(i int) bool { return !t.WordEnd || wordEndAt(s, i+len(last)) }
	if len(parts) == 1 {
		return find(s, 0, first, func(i int) bool { return startOK(i) && endOK(i) }) >= 0
	}
	i := find(s, 0, first, startOK)
	if i < 0 {
		return false
	}
	pos := i + len(first)
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s[pos:], p)
		if i < 0 {
			return false
		}
		pos += i + len(p)
	}
	return find(s, pos, last, endOK) >= 0
}

// find returns the leftmost occurrence of p in s at or after from that
// satisfies ok, or -1.
func find(s string, from int, p string, ok func(i int) bool) int {
	for from <= len(s) {
		i := strings.Index(s[from:], p)
		if i < 0 {
			return -1
		}
		if ok(from + i) {
			return from + i
		}
		from += i + 1
	}
	return -1
}

// isWordRune: a word is a run of Unicode letters or digits; everything else,
// `_ - . / :` included, separates words.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordStartAt reports whether a word may start at byte i of s: at the line
// start or after a non-word rune.
func wordStartAt(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !isWordRune(r)
}

// wordEndAt reports whether a word may end at byte j of s: at the line end or
// before a non-word rune.
func wordEndAt(s string, j int) bool {
	if j == len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[j:])
	return !isWordRune(r)
}
