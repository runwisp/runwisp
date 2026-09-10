// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify_test

import (
	"testing"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
	"github.com/stretchr/testify/assert"
)

func TestRouter_DeduplicatesActionsAcrossRules(t *testing.T) {
	a := testutil.NewFakeChannel("slack:ops")
	channels := map[string]notify.Channel{a.ID(): a}
	router := notify.NewRouter([]notify.Rule{
		{Match: notify.MatchFailure(), ActionIDs: []string{"slack:ops"}},
		{Match: notify.MatchTaskGlob("*"), ActionIDs: []string{"slack:ops"}},
	}, channels)

	out := router.Route(&notify.Event{Kind: notify.KindRunFailed, IsFailure: true, TaskName: "backup-db"})
	assert.Len(t, out, 1, "duplicate action across rules must be deduped")
}

func TestRouter_SkipsUnknownActions(t *testing.T) {
	a := testutil.NewFakeChannel("slack:ops")
	channels := map[string]notify.Channel{a.ID(): a}
	router := notify.NewRouter([]notify.Rule{
		{Match: notify.MatchAll(), ActionIDs: []string{"slack:ops", "missing"}},
	}, channels)

	out := router.Route(&notify.Event{Kind: notify.KindRunSucceeded, Severity: notify.SevInfo})
	assert.Len(t, out, 1)
	assert.Equal(t, "slack:ops", out[0].ID())
}

func TestPredicate_TaskGlob(t *testing.T) {
	p := notify.MatchTaskGlob("backup-*", "etl-?")
	assert.True(t, p(&notify.Event{TaskName: "backup-db"}))
	assert.True(t, p(&notify.Event{TaskName: "etl-1"}))
	assert.False(t, p(&notify.Event{TaskName: "cleanup"}))
}
