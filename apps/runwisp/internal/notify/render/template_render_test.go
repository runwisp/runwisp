// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify"
)

func eventTime(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Bratislava")
	if err != nil {
		t.Skip("tz data unavailable")
	}
	return time.Date(2026, 5, 14, 17, 11, 0, 0, loc)
}

func makeLogTail(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")
	lines := []string{
		"Error: connection refused",
		"dial tcp 127.0.0.1:5432: connect:",
		"connection refused",
	}
	var buf strings.Builder
	for _, l := range lines {
		buf.WriteString(logutil.FormatLine(l, logutil.StreamStdout))
	}
	require.NoError(t, os.WriteFile(path, []byte(buf.String()), 0o600))
	return path
}

func renderTelegram(t *testing.T, ctx TemplateContext, ev *notify.Event) string {
	t.Helper()
	body, err := LoadDefaultTemplate("telegram")
	require.NoError(t, err)
	r, err := NewTemplateRendererWithContext("telegram:test", body, "text/html", DefaultTitle, ctx)
	require.NoError(t, err)
	out, err := r.Render(ev)
	require.NoError(t, err)
	return string(out.Body)
}

func renderSlack(t *testing.T, ctx TemplateContext, ev *notify.Event) string {
	t.Helper()
	body, err := LoadDefaultTemplate("slack")
	require.NoError(t, err)
	r, err := NewTemplateRendererWithContext("slack:test", body, "application/json", DefaultTitle, ctx)
	require.NoError(t, err)
	out, err := r.Render(ev)
	require.NoError(t, err)
	return string(out.Body)
}

func TestTelegram_RunFailed_WithURLAndTail(t *testing.T) {
	logPath := makeLogTail(t)
	start := eventTime(t)
	end := start.Add(300 * time.Millisecond)
	run := &model.Run{
		ID:          "01KRK9SS7MKEWN4F49R30ZM74N",
		TaskName:    "telegram-test-fail",
		ExitCode:    1,
		StartAt:     &start,
		EndAt:       &end,
		TriggeredBy: model.TriggeredByAPI,
	}
	ev := &notify.Event{
		Kind:      notify.KindRunFailed,
		Severity:  notify.SevError,
		Timestamp: start,
		TaskName:  "telegram-test-fail",
		Run:       run,
		LogPath:   logPath,
	}
	ctx := TemplateContext{
		ExternalURL: "https://runwisp.example.com",
		Fingerprint: "bright-falcon",
		OutputTail:  NewOutputTail(),
	}
	got := renderTelegram(t, ctx, ev)
	expected := "❌ <b>telegram-test-fail</b> failed\n" +
		"\n" +
		"Exited with code 1 after 0.3s.\n" +
		"Manually triggered via API · 14 May, 17:11.\n" +
		"\n" +
		"<blockquote>Error: connection refused\ndial tcp 127.0.0.1:5432: connect:\nconnection refused</blockquote>\n" +
		"\n" +
		"🔗 <a href=\"https://runwisp.example.com/tasks/telegram-test-fail?runId=01KRK9SS7MKEWN4F49R30ZM74N\">View full run</a>\n" +
		"\n" +
		"<i>from runwisp · bright-falcon</i>\n"
	assert.Equal(t, expected, got)
}

func TestTelegram_RunFailed_NoURL_NoTail(t *testing.T) {
	start := eventTime(t)
	end := start.Add(300 * time.Millisecond)
	run := &model.Run{
		ID:          "01KRK9SS7MKEWN4F49R30ZM74N",
		TaskName:    "telegram-test-fail",
		ExitCode:    1,
		StartAt:     &start,
		EndAt:       &end,
		TriggeredBy: model.TriggeredByAPI,
	}
	ev := &notify.Event{
		Kind:      notify.KindRunFailed,
		Severity:  notify.SevError,
		Timestamp: start,
		TaskName:  "telegram-test-fail",
		Run:       run,
	}
	ctx := TemplateContext{
		Fingerprint: "bright-falcon",
		OutputTail:  NewOutputTail(),
	}
	got := renderTelegram(t, ctx, ev)
	assert.NotContains(t, got, "🔗", "link line must be omitted when external_url is unset")
	assert.NotContains(t, got, "<blockquote>", "blockquote must be omitted when log tail is empty")
	assert.Contains(t, got, "<b>telegram-test-fail</b> failed")
	assert.Contains(t, got, "Exited with code 1 after 0.3s.")
	assert.Contains(t, got, "<i>from runwisp · bright-falcon</i>")
}

func TestTelegram_RunSucceeded(t *testing.T) {
	start := eventTime(t)
	end := start.Add(12*time.Second + 400*time.Millisecond)
	run := &model.Run{
		ID:          "01KRK9",
		TaskName:    "nightly-backup",
		StartAt:     &start,
		EndAt:       &end,
		TriggeredBy: model.TriggeredByCron,
	}
	ev := &notify.Event{
		Kind:      notify.KindRunSucceeded,
		Severity:  notify.SevInfo,
		Timestamp: start,
		TaskName:  "nightly-backup",
		Run:       run,
	}
	ctx := TemplateContext{
		ExternalURL: "https://r.example.com",
		Fingerprint: "bright-falcon",
		OutputTail:  NewOutputTail(),
	}
	got := renderTelegram(t, ctx, ev)
	assert.Contains(t, got, "✅ <b>nightly-backup</b> succeeded")
	assert.Contains(t, got, "Completed in 12s.")
	assert.Contains(t, got, "Scheduled run · 14 May, 17:11.")
	assert.Contains(t, got, "View run</a>")
	assert.NotContains(t, got, "<blockquote>", "no captured-output tail for successful runs")
}

func TestTelegram_RunTimeout_HasTail(t *testing.T) {
	logPath := makeLogTail(t)
	start := eventTime(t)
	end := start.Add(5 * time.Minute)
	run := &model.Run{
		ID:          "01KT",
		TaskName:    "long-task",
		StartAt:     &start,
		EndAt:       &end,
		TriggeredBy: model.TriggeredByCron,
	}
	ev := &notify.Event{
		Kind:      notify.KindRunTimeout,
		Severity:  notify.SevError,
		Timestamp: start,
		TaskName:  "long-task",
		Run:       run,
		LogPath:   logPath,
	}
	ctx := TemplateContext{
		ExternalURL: "https://r.example.com",
		Fingerprint: "bright-falcon",
		OutputTail:  NewOutputTail(),
	}
	got := renderTelegram(t, ctx, ev)
	assert.Contains(t, got, "⏱️ <b>long-task</b> timed out")
	assert.Contains(t, got, "The task was killed after the configured timeout (5m elapsed).")
	assert.Contains(t, got, "<blockquote>")
}

func TestTelegram_RunCrashed_NoRun(t *testing.T) {
	ev := &notify.Event{
		Kind:      notify.KindRunCrashed,
		Severity:  notify.SevError,
		Timestamp: eventTime(t),
		TaskName:  "doomed",
		Reason:    "exec format error",
	}
	ctx := TemplateContext{
		ExternalURL: "https://r.example.com",
		Fingerprint: "bright-falcon",
		OutputTail:  NewOutputTail(),
	}
	got := renderTelegram(t, ctx, ev)
	assert.Contains(t, got, "💥 <b>doomed</b> crashed")
	assert.Contains(t, got, "The process couldn't start: exec format error.")
	assert.NotContains(t, got, "🔗", "no run link without a Run")
	assert.Contains(t, got, "<i>from runwisp · bright-falcon</i>")
}

func TestTelegram_LogDiskPressure_TaskLink(t *testing.T) {
	ev := &notify.Event{
		Kind:      notify.KindLogDiskPressure,
		Severity:  notify.SevWarn,
		Timestamp: eventTime(t),
		TaskName:  "noisy-task",
	}
	ctx := TemplateContext{
		ExternalURL: "https://r.example.com",
		Fingerprint: "bright-falcon",
		OutputTail:  NewOutputTail(),
	}
	got := renderTelegram(t, ctx, ev)
	assert.Contains(t, got, "💾 <b>noisy-task</b> log output paused")
	assert.Contains(t, got, "Disk pressure is high")
	assert.Contains(t, got, "🔗 <a href=\"https://r.example.com/tasks/noisy-task\">Open task</a>")
}

func TestTelegram_EscapesUntrustedFields(t *testing.T) {
	ev := &notify.Event{
		Kind:      notify.KindRunCrashed,
		Severity:  notify.SevError,
		Timestamp: eventTime(t),
		TaskName:  "evil<script>",
		Reason:    "boom & <b>bold</b>",
	}
	ctx := TemplateContext{Fingerprint: "fp&me"}
	got := renderTelegram(t, ctx, ev)
	assert.Contains(t, got, "&lt;script&gt;")
	assert.Contains(t, got, "&amp;")
	assert.Contains(t, got, "&lt;b&gt;bold&lt;/b&gt;")
	assert.Contains(t, got, "fp&amp;me")
}

func TestSlack_RunFailed_WithURLAndTail(t *testing.T) {
	logPath := makeLogTail(t)
	start := eventTime(t)
	end := start.Add(300 * time.Millisecond)
	run := &model.Run{
		ID:          "01KRK9",
		TaskName:    "tg-fail",
		ExitCode:    1,
		StartAt:     &start,
		EndAt:       &end,
		TriggeredBy: model.TriggeredByAPI,
	}
	ev := &notify.Event{
		Kind:      notify.KindRunFailed,
		Severity:  notify.SevError,
		Timestamp: start,
		TaskName:  "tg-fail",
		Run:       run,
		LogPath:   logPath,
	}
	ctx := TemplateContext{
		ExternalURL: "https://r.example.com",
		Fingerprint: "bright-falcon",
		OutputTail:  NewOutputTail(),
	}
	got := renderSlack(t, ctx, ev)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &parsed), "rendered slack body must be valid JSON:\n%s", got)
	blocks, ok := parsed["blocks"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, blocks)

	concatTexts := slackTexts(blocks)
	assert.Contains(t, concatTexts, "❌ tg-fail failed")
	assert.Contains(t, concatTexts, "Exited with code 1 after 0.3s.\nManually triggered via API · 14 May, 17:11.")
	assert.Contains(t, concatTexts, "```\nError: connection refused\ndial tcp 127.0.0.1:5432: connect:\nconnection refused\n```")
	assert.Contains(t, concatTexts, "View full run")
	assert.Contains(t, concatTexts, "from runwisp · bright-falcon")

	// Check the action block carries the expected URL.
	foundButton := false
	for _, b := range blocks {
		obj, _ := b.(map[string]any)
		if obj["type"] != "actions" {
			continue
		}
		elems, _ := obj["elements"].([]any)
		for _, e := range elems {
			el, _ := e.(map[string]any)
			if el["url"] == "https://r.example.com/tasks/tg-fail?runId=01KRK9" {
				foundButton = true
			}
		}
	}
	assert.True(t, foundButton, "expected action button with run URL")
}

func TestSlack_RunFailed_NoURL(t *testing.T) {
	start := eventTime(t)
	end := start.Add(300 * time.Millisecond)
	run := &model.Run{ID: "01KRK9", TaskName: "tg-fail", ExitCode: 1, StartAt: &start, EndAt: &end, TriggeredBy: model.TriggeredByAPI}
	ev := &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, Timestamp: start, TaskName: "tg-fail", Run: run}
	got := renderSlack(t, TemplateContext{Fingerprint: "fp"}, ev)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &parsed), got)
	blocks := parsed["blocks"].([]any)
	for _, b := range blocks {
		obj, _ := b.(map[string]any)
		assert.NotEqual(t, "actions", obj["type"], "no action block when external_url is unset")
	}
}

func slackTexts(blocks []any) string {
	var b strings.Builder
	for _, blk := range blocks {
		obj, _ := blk.(map[string]any)
		if text, ok := obj["text"].(map[string]any); ok {
			if s, ok := text["text"].(string); ok {
				b.WriteString(s)
				b.WriteString("\n")
			}
		}
		if elems, ok := obj["elements"].([]any); ok {
			for _, e := range elems {
				el, _ := e.(map[string]any)
				if text, ok := el["text"].(map[string]any); ok {
					if s, ok := text["text"].(string); ok {
						b.WriteString(s)
						b.WriteString("\n")
					}
				}
				if s, ok := el["text"].(string); ok {
					b.WriteString(s)
					b.WriteString("\n")
				}
			}
		}
	}
	return b.String()
}
