// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configload

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/render"
)

func TestResolve_NotifiersCarryConfigAndRenderContext(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{
			{ID: "ops", Type: "slack", WebhookURL: "https://hooks.slack.test/T/B/Z"},
			{ID: "hook", Type: "webhook", URL: "https://example.com/hook", Headers: map[string]string{"X": "y"}},
		},
	}
	got := Resolve(cfg, render.TemplateContext{ExternalURL: func() string { return "https://rw.test" }})
	require.Len(t, got.Notifiers, 2)
	assert.Equal(t, cfg.Notifiers[0], got.Notifiers[0].NotifierSpec)
	assert.Equal(t, cfg.Notifiers[1], got.Notifiers[1].NotifierSpec)
	assert.Equal(t, "https://rw.test", got.Notifiers[0].RenderContext.ExternalURL())
}

func TestResolve_CompiledRulePredicates(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "https://hooks.slack.test/T/B/Z",
		}},
		Routes: []config.NotificationRoute{{
			Kinds:      []string{"failed"},
			TaskGlob:   "backup-*",
			NotifierID: []string{"ops"},
		}},
	}
	got := Resolve(cfg, render.TemplateContext{})
	require.Len(t, got.Rules, 1)
	require.Equal(t, []string{"ops"}, got.Rules[0].ActionIDs)

	matches := got.Rules[0].Match
	require.NotNil(t, matches)

	failed := model.ReasonFailed
	succeeded := model.ReasonSuccess

	yes := &notify.Event{Kind: notify.KindRunFailed, TaskName: "backup-db", Run: &model.Run{EndReason: &failed}}
	assert.True(t, matches(yes))

	wrongKind := &notify.Event{Kind: notify.KindRunSucceeded, TaskName: "backup-db", Run: &model.Run{EndReason: &succeeded}}
	assert.False(t, matches(wrongKind))

	wrongTask := &notify.Event{Kind: notify.KindRunFailed, TaskName: "etl-1", Run: &model.Run{EndReason: &failed}}
	assert.False(t, matches(wrongTask))
}
