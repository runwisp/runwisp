// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/kinds"
)

func TestStatusEmojiAndVerbCoverAllKinds(t *testing.T) {
	for _, k := range kinds.AllKindStrings {
		assert.NotEmpty(t, statusEmoji(notify.Kind(k)), "kind %s missing emoji", k)
		assert.NotEmpty(t, statusVerb(notify.Kind(k)), "kind %s missing verb", k)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{-time.Second, "0s"},
		{300 * time.Millisecond, "0.3s"},
		{1500 * time.Millisecond, "2s"}, // rounds to nearest second
		{12 * time.Second, "12s"},
		{59 * time.Second, "59s"},
		{61 * time.Second, "1m 1s"},
		{2*time.Minute + 30*time.Second, "2m 30s"},
		{5 * time.Minute, "5m"},
		{time.Hour, "1h"},
		{75 * time.Minute, "1h 15m"},
	}
	for _, c := range cases {
		got := humanDuration(c.in)
		assert.Equal(t, c.want, got, "humanDuration(%s)", c.in)
	}
}

func TestHumanTime(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bratislava")
	if err != nil {
		t.Skip("tz data unavailable")
	}
	got := humanTime(time.Date(2026, 5, 14, 17, 11, 0, 0, loc))
	assert.Equal(t, "14 May, 17:11", got)
	assert.Equal(t, "", humanTime(time.Time{}))
}

func TestTriggerPhrase(t *testing.T) {
	assert.Equal(t, "Scheduled run", triggerPhrase(model.TriggeredByCron))
	assert.Equal(t, "Manually triggered via API", triggerPhrase(model.TriggeredByAPI))
	assert.Equal(t, "Triggered from the control plane", triggerPhrase(model.TriggeredByCloud))
	assert.Equal(t, "Service auto-started", triggerPhrase(model.TriggeredByService))
	assert.Equal(t, "Run", triggerPhrase(""))
}

func TestRunURL(t *testing.T) {
	r := &model.Run{ID: "01KRK", TaskName: "nightly-backup"}
	assert.Equal(t, "", runURL("", r))
	assert.Equal(t, "", runURL("https://x", nil))
	assert.Equal(t, "", runURL("https://x", &model.Run{ID: "01KRK"}))
	assert.Equal(t, "", runURL("https://x", &model.Run{TaskName: "n"}))
	assert.Equal(t, "https://x/tasks/nightly-backup/01KRK", runURL("https://x", r))

	// Path-escape the task name (spec disallows spaces but be defensive).
	tricky := &model.Run{ID: "01", TaskName: "name with space"}
	assert.Equal(t, "https://x/tasks/name%20with%20space/01", runURL("https://x", tricky))
}

func TestTaskURL(t *testing.T) {
	assert.Equal(t, "", taskURL("", "x"))
	assert.Equal(t, "", taskURL("https://x", ""))
	assert.Equal(t, "https://x/tasks/foo", taskURL("https://x", "foo"))
}

func TestRunDuration(t *testing.T) {
	start := time.Date(2026, 5, 14, 17, 11, 0, 0, time.UTC)
	end := start.Add(12*time.Second + 400*time.Millisecond)
	r := &model.Run{StartedAt: &start, EndedAt: &end}
	assert.Equal(t, "12s", runDuration(r))

	assert.Equal(t, "", runDuration(nil))
	assert.Equal(t, "", runDuration(&model.Run{StartedAt: &start}))
	assert.Equal(t, "", runDuration(&model.Run{EndedAt: &end}))
}

func TestEventSentence(t *testing.T) {
	start := time.Date(2026, 5, 14, 17, 11, 0, 0, time.UTC)
	end := start.Add(300 * time.Millisecond)
	withDur := &model.Run{StartedAt: &start, EndedAt: &end, ExitCode: 1}
	noDur := &model.Run{ExitCode: 2}

	cases := []struct {
		name string
		ev   *notify.Event
		want string
	}{
		{"failed with run + duration", &notify.Event{Kind: notify.KindRunFailed, Run: withDur}, "Exited with code 1 after 0.3s."},
		{"failed with run, no duration", &notify.Event{Kind: notify.KindRunFailed, Run: noDur}, "Exited with code 2."},
		{"failed without run", &notify.Event{Kind: notify.KindRunFailed}, "Exited with code ?."},
		{"succeeded with duration", &notify.Event{Kind: notify.KindRunSucceeded, Run: withDur}, "Completed in 0.3s."},
		{"succeeded without duration", &notify.Event{Kind: notify.KindRunSucceeded}, "Completed."},
		{"timeout with duration", &notify.Event{Kind: notify.KindRunTimeout, Run: withDur}, "The task was killed after the configured timeout (0.3s elapsed)."},
		{"timeout without duration", &notify.Event{Kind: notify.KindRunTimeout}, "The task was killed after the configured timeout."},
		{"stopped with duration", &notify.Event{Kind: notify.KindRunStopped, Run: withDur}, "Stopped manually after 0.3s."},
		{"stopped without duration", &notify.Event{Kind: notify.KindRunStopped}, "Stopped manually."},
		{"crashed with reason", &notify.Event{Kind: notify.KindRunCrashed, Reason: "exec format error"}, "The process couldn't start: exec format error."},
		{"crashed without reason", &notify.Event{Kind: notify.KindRunCrashed}, "The process couldn't start."},
		{"missed with reason", &notify.Event{Kind: notify.KindRunMissed, Reason: "3 scheduled runs missed since 2026-06-09 03:00 (daemon was down)"}, "3 scheduled runs missed since 2026-06-09 03:00 (daemon was down)"},
		{"missed without reason", &notify.Event{Kind: notify.KindRunMissed}, "A scheduled run was missed while the daemon was down."},
		{"started", &notify.Event{Kind: notify.KindRunStarted}, "Run started."},
		{"log disk pressure", &notify.Event{Kind: notify.KindLogDiskPressure}, "Disk pressure is high; log capture is paused for this task until disk space is recovered."},
		{"delivery failed with reason", &notify.Event{Kind: notify.KindNotifyDeliveryFailed, Reason: "timeout"}, "A notification could not be delivered: timeout."},
		{"delivery failed without reason", &notify.Event{Kind: notify.KindNotifyDeliveryFailed}, "A notification could not be delivered."},
		{"unknown kind with reason", &notify.Event{Kind: notify.Kind("custom.kind"), Reason: "something"}, "something"},
		{"unknown kind fallback", &notify.Event{Kind: notify.Kind("custom.kind")}, "custom.kind."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, eventSentence(c.ev))
		})
	}
}

// TestEventSentenceCoalescedCount proves a delivery that folded repeats (the
// outbound coalescer stamps coalesced_count on Extra) discloses the fold in the
// rendered body. Without it a flap folded into one alert reads identically to a
// single occurrence — a silent loss.
func TestEventSentenceCoalescedCount(t *testing.T) {
	base := &notify.Event{Kind: notify.KindRunFailed}
	assert.Equal(t, "Exited with code ?.", eventSentence(base), "no Extra means no suffix")

	folded := &notify.Event{Kind: notify.KindRunFailed, Extra: map[string]any{"coalesced_count": 5}}
	assert.Equal(t, "Exited with code ?. (×5 — earlier repeats coalesced into this alert)", eventSentence(folded))

	// A count of one is a lone event, not a fold: no suffix.
	single := &notify.Event{Kind: notify.KindRunFailed, Extra: map[string]any{"coalesced_count": 1}}
	assert.Equal(t, "Exited with code ?.", eventSentence(single))
}

func TestEventTrigger(t *testing.T) {
	assert.Equal(t, "Event", eventTrigger(&notify.Event{}))
	assert.Equal(t, "Scheduled run", eventTrigger(&notify.Event{Run: &model.Run{TriggeredBy: model.TriggeredByCron}}))
	assert.Equal(t, "Manually triggered via API", eventTrigger(&notify.Event{Run: &model.Run{TriggeredBy: model.TriggeredByAPI}}))
	assert.Equal(t, "Triggered from the control plane", eventTrigger(&notify.Event{Run: &model.Run{TriggeredBy: model.TriggeredByCloud}}))
	assert.Equal(t, "Run", eventTrigger(&notify.Event{Run: &model.Run{}}))
}

func TestLinkLabel(t *testing.T) {
	cases := map[notify.Kind]string{
		notify.KindRunFailed:            "View full run",
		notify.KindLogDiskPressure:      "Open task",
		notify.KindRunSucceeded:         "View run",
		notify.KindRunTimeout:           "View run",
		notify.KindRunStopped:           "View run",
		notify.KindRunCrashed:           "View run",
		notify.KindRunMissed:            "View run",
		notify.KindRunStarted:           "View run",
		notify.KindNotifyDeliveryFailed: "View run",
	}
	for k, want := range cases {
		assert.Equal(t, want, linkLabel(k), "kind %s", k)
	}
}
