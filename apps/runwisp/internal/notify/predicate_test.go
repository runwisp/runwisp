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
