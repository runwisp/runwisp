// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package fingerprint

import (
	"testing"
)

func TestGenerateDeterministic(t *testing.T) {
	a := Generate()
	b := Generate()
	if a != b {
		t.Errorf("expected deterministic output, got %q and %q", a, b)
	}
	if a == "" {
		t.Error("expected non-empty fingerprint")
	}
}

func TestGenerateFormat(t *testing.T) {
	fp := Generate()
	parts := 0
	for _, c := range fp {
		if c == '-' {
			parts++
		}
	}
	if parts != 1 {
		t.Errorf("expected exactly one hyphen in %q", fp)
	}
}
