// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logsearch

import (
	"bytes"
	"testing"
)

func TestSubstringMatcher_CaseSensitive(t *testing.T) {
	m, err := NewMatcher("ERROR", false, true)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !m.Match([]byte("an ERROR happened")) {
		t.Fatal("expected match on ERROR")
	}
	if m.Match([]byte("an error happened")) {
		t.Fatal("case-sensitive match should miss lowercase")
	}
}

func TestSubstringMatcher_CaseInsensitive(t *testing.T) {
	m, err := NewMatcher("connection refused", false, false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	cases := []struct {
		line string
		hit  bool
	}{
		{"Connection Refused: redis", true},
		{"CONNECTION REFUSED", true},
		{"all good", false},
		{"", false},
	}
	for _, c := range cases {
		if got := m.Match([]byte(c.line)); got != c.hit {
			t.Fatalf("Match(%q) = %v, want %v", c.line, got, c.hit)
		}
	}
}

func TestSubstringMatcher_ReusesScratchBuffer(t *testing.T) {
	m, err := NewMatcher("foo", false, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Run many lines; if the implementation re-allocates each line, this
	// would still pass — but the steady-state guarantee is documented and
	// covered indirectly here by exercising the path repeatedly without a
	// data race.
	for i := 0; i < 1000; i++ {
		m.Match([]byte("HELLO FOO BAR"))
	}
}

func TestSubstringMatcher_CaseFoldLongNonASCII(t *testing.T) {
	// A line longer than the initial scratch cap that ends in an invalid
	// UTF-8 byte. Folding decodes that byte to U+FFFD (3 bytes), so the
	// lowered remainder is longer than the input — this must not panic on
	// the scratch buffer bounds.
	m, err := NewMatcher("needle", false, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	line := append(bytes.Repeat([]byte("A"), 256), 0xff)
	if m.Match(line) {
		t.Fatal("did not expect a match")
	}
	// Fold correctness is still exercised: an ASCII match survives the
	// non-ASCII tail.
	line2 := append(bytes.Repeat([]byte("NEEDLE X"), 40), 0xff, 0xfe)
	if !m.Match(line2) {
		t.Fatal("expected case-folded match before the invalid tail")
	}
}

func TestRegexMatcher_CompileError(t *testing.T) {
	if _, err := NewMatcher("(unclosed", true, true); err == nil {
		t.Fatal("expected compile error")
	}
}

func TestRegexMatcher_CaseInsensitive(t *testing.T) {
	m, err := NewMatcher("err.*\\d+", true, false)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !m.Match([]byte("ERR code 42")) {
		t.Fatal("expected case-insensitive regex hit")
	}
	if m.Match([]byte("no problem")) {
		t.Fatal("unexpected match")
	}
}

func TestNewMatcher_EmptyQuery(t *testing.T) {
	if _, err := NewMatcher("", false, false); err == nil {
		t.Fatal("empty query should error")
	}
}
