// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify"
)

// renderPush renders a push template (ntfy, gotify, pushover) and decodes it
// into a generic map, failing the test when the output isn't valid JSON.
func renderPush(t *testing.T, kind string, ctx TemplateContext, ev *notify.Event) map[string]any {
	t.Helper()
	body, err := LoadDefaultTemplate(kind)
	require.NoError(t, err)
	r, err := NewTemplateRendererWithContext(kind+":test", body, "application/json", DefaultTitle, ctx)
	require.NoError(t, err)
	out, err := r.Render(ev)
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(out.Body, &parsed), "rendered %s body must be valid JSON:\n%s", kind, out.Body)
	return parsed
}

// pushClickURL pulls the run link out of each provider's native click field.
func pushClickURL(kind string, p map[string]any) any {
	switch kind {
	case "ntfy":
		return p["click"]
	case "gotify":
		extras, _ := p["extras"].(map[string]any)
		notif, _ := extras["client::notification"].(map[string]any)
		click, _ := notif["click"].(map[string]any)
		return click["url"]
	default:
		return p["url"]
	}
}

var pushPriorities = map[string]struct{ err, warn, info float64 }{
	"ntfy":     {4, 3, 2},
	"gotify":   {8, 5, 2},
	"pushover": {0, 0, -1},
}

func TestPushTemplates_RunFailed_WithURLAndTail(t *testing.T) {
	start := eventTime(t)
	end := start.Add(300 * time.Millisecond)
	run := &model.Run{ID: "01KRK9", TaskName: "dc-fail", ExitCode: 1, StartedAt: &start, EndedAt: &end, TriggeredBy: model.TriggeredByAPI}
	ev := &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, Timestamp: start, TaskName: "dc-fail", Run: run, LogPath: makeLogTail(t)}
	ctx := TemplateContext{ExternalURL: "https://r.example.com", Fingerprint: "bright-falcon", OutputTail: NewOutputTail()}

	for kind, prio := range pushPriorities {
		t.Run(kind, func(t *testing.T) {
			p := renderPush(t, kind, ctx, ev)
			assert.Equal(t, "❌ dc-fail failed", p["title"])
			assert.Equal(t, "Exited with code 1 after 0.3s.\nTriggered via the REST API · 14 May, 17:11.\n\n"+
				"Error: connection refused\ndial tcp 127.0.0.1:5432: connect:\nconnection refused\n\n"+
				"from runwisp · bright-falcon", p["message"])
			assert.Equal(t, prio.err, p["priority"])
			assert.Equal(t, "https://r.example.com/tasks/dc-fail/01KRK9", pushClickURL(kind, p))
		})
	}
}

func TestPushTemplates_PriorityBySeverity_NoURL(t *testing.T) {
	start := eventTime(t)
	succeeded := &notify.Event{Kind: notify.KindRunSucceeded, Severity: notify.SevInfo, Timestamp: start, TaskName: "backup"}
	pressure := &notify.Event{Kind: notify.KindLogDiskPressure, Severity: notify.SevWarn, Timestamp: start, TaskName: "noisy"}

	for kind, prio := range pushPriorities {
		t.Run(kind, func(t *testing.T) {
			p := renderPush(t, kind, TemplateContext{}, succeeded)
			assert.Equal(t, prio.info, p["priority"])
			assert.Nil(t, pushClickURL(kind, p), "no click link when external_url is unset")
			assert.Equal(t, prio.warn, renderPush(t, kind, TemplateContext{}, pressure)["priority"])
		})
	}
}

func TestPushTemplates_EscapeUntrustedFields(t *testing.T) {
	ev := &notify.Event{Kind: notify.KindRunCrashed, Severity: notify.SevError, Timestamp: eventTime(t), TaskName: `evil"task\name`, Reason: "boom \"quoted\""}
	for kind := range pushPriorities {
		t.Run(kind, func(t *testing.T) {
			p := renderPush(t, kind, TemplateContext{}, ev)
			assert.Contains(t, p["title"], `evil"task\name`)
		})
	}
}
