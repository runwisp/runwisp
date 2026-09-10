// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify_test

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify"
)

// TestMatchOutcomes matches on the fine-grained end reason, distinguishing
// outcomes the SSE Kind collapses: a log_overflow run (Kind run.failed) matches
// only "log_overflow", not "failed".
func TestMatchOutcomes(t *testing.T) {
	overflow := model.ReasonLogOverflow
	ev := &notify.Event{Kind: notify.KindRunFailed, Run: &model.Run{EndReason: &overflow}}

	if !notify.MatchOutcomes("log_overflow")(ev) {
		t.Fatal("MatchOutcomes must match on the fine-grained end reason")
	}
	if notify.MatchOutcomes("failed")(ev) {
		t.Fatal("a log_overflow run must not match the bare failed token")
	}
	if !notify.MatchOutcomes("service.fatal")(&notify.Event{Kind: notify.KindServiceFatal}) {
		t.Fatal("MatchOutcomes must match a non-run event by its kind token")
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
