// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify_test

import (
	"testing"

	"github.com/runwisp/runwisp/internal/notify"
)

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

// TestMatchFailure gates the built-in failure route on the classified bit: a
// promoted stopped run (IsFailure=true) matches while a demoted missed run
// (IsFailure=false) does not, regardless of Kind.
func TestMatchFailure(t *testing.T) {
	pred := notify.MatchFailure()
	if !pred(&notify.Event{Kind: notify.KindRunStopped, IsFailure: true}) {
		t.Fatal("MatchFailure must match a run classified as a failure (promoted stopped)")
	}
	if pred(&notify.Event{Kind: notify.KindRunMissed, IsFailure: false}) {
		t.Fatal("MatchFailure must not match a run not classified as a failure (demoted missed)")
	}
}
