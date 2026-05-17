// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify_test

import (
	"testing"

	"github.com/runwisp/runwisp/internal/notify"
)

// testEvent is a non-nil event suitable for passing to predicates.
var testEvent = &notify.Event{
	Kind:     notify.KindRunFailed,
	TaskName: "my-task",
	Severity: notify.SevError,
}

func TestOr(t *testing.T) {
	matchNone := notify.MatchKind("__never__")
	matchAll := notify.MatchAll()
	tests := []struct {
		name string
		a, b notify.Predicate
		want bool
	}{
		{"both false", matchNone, matchNone, false},
		{"first true", matchAll, matchNone, true},
		{"second true", matchNone, matchAll, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notify.Or(tt.a, tt.b)(testEvent); got != tt.want {
				t.Fatalf("Or = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNot(t *testing.T) {
	matchAll := notify.MatchAll()
	matchNone := notify.MatchKind("__never__")
	tests := []struct {
		name string
		p    notify.Predicate
		want bool
	}{
		{"inverts true", matchAll, false},
		{"inverts false", matchNone, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notify.Not(tt.p)(testEvent); got != tt.want {
				t.Fatalf("Not = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidGlob_InvalidBracket(t *testing.T) {
	if notify.ValidGlob("[invalid") {
		t.Fatal("ValidGlob('[invalid') must return false for invalid bracket expression")
	}
}

func TestMatchSeverity_EventUnknownSeverity_ReturnsFalse(t *testing.T) {
	pred := notify.MatchSeverity(notify.SevError)
	ev := &notify.Event{
		Kind:     notify.KindRunFailed,
		TaskName: "my-task",
		Severity: "unknown-sev",
	}
	if pred(ev) {
		t.Fatal("MatchSeverity(error) must return false when event has unknown severity")
	}
}
