// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package clilog

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLogger(h slog.Handler) *slog.Logger { return slog.New(h) }

func TestPrettyHandler_PlainHasNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(newPrettyHandler(&buf, slog.LevelInfo, false, false))

	log.Info("ready, listening", "url", "http://127.0.0.1:9477")

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "color off must emit no escape bytes")
	assert.Contains(t, out, "[INFO]")
	assert.Contains(t, out, "ready, listening")
	assert.Contains(t, out, "url=http://127.0.0.1:9477")
}

func TestPrettyHandler_ColorEmitsEscapes(t *testing.T) {
	// lipgloss resolves the global color profile at render time; a non-TTY test
	// env defaults to ASCII (no escapes), so force a colored profile here.
	forceColorProfile(t)
	var buf bytes.Buffer
	log := newTestLogger(newPrettyHandler(&buf, slog.LevelInfo, false, true))

	log.Warn("disk getting full", "pct", 91)

	out := buf.String()
	assert.Contains(t, out, "\x1b", "color on must style the line")
	// Content survives styling (the key and value are styled separately, so the
	// raw line splits "pct=" from "91" with an escape — assert each piece).
	assert.Contains(t, out, "disk getting full")
	assert.Contains(t, out, "pct=")
	assert.Contains(t, out, "91")
}

func TestPrettyHandler_QuotesValuesWithSpaces(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(newPrettyHandler(&buf, slog.LevelInfo, false, false))

	log.Info("init warning", "detail", "two words")

	assert.Contains(t, buf.String(), `detail="two words"`)
}

// Regression (Bug 5): attribute values reach the pretty handler from untrusted
// input (e.g. a task name in an HTTP/WS body). A bare ESC (0x1b) or other control
// byte written raw to a terminal is an escape-injection vector, so the value must
// be Go-quoted (control bytes rendered as visible \x escapes), never emitted raw.
func TestPrettyHandler_EscapesControlBytesInAttrValue(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(newPrettyHandler(&buf, slog.LevelInfo, false, false))

	log.Info("search", "task", "evil\x1b[2Jname")

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "a raw ESC in an attr value must never reach the terminal")
	assert.Contains(t, out, `\x1b`, "the control byte must be rendered as a visible escape")
}

// Regression (Bug 5): the record message is painted inline (not quoted) but can
// also carry untrusted data, so control bytes there are escaped too.
func TestPrettyHandler_EscapesControlBytesInMessage(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(newPrettyHandler(&buf, slog.LevelInfo, false, false))

	log.Info("boot\x1b[31mred")

	out := buf.String()
	assert.NotContains(t, out, "\x1b", "a raw ESC in the message must never reach the terminal")
	assert.Contains(t, out, `\x1b`, "the control byte must be rendered as a visible escape")
}

func TestPrettyHandler_AttrsAndGroups(t *testing.T) {
	var buf bytes.Buffer
	h := newPrettyHandler(&buf, slog.LevelInfo, false, false)
	log := newTestLogger(h).
		With("version", "0.11.0").
		WithGroup("scheduler").
		With("tz", "Europe/Bratislava")

	log.Info("starting", "tasks", 1)

	out := buf.String()
	assert.Contains(t, out, "version=0.11.0", "WithAttrs before a group keeps a bare key")
	assert.Contains(t, out, "scheduler.tz=Europe/Bratislava", "WithGroup must prefix nested attrs")
	assert.Contains(t, out, "scheduler.tasks=1", "record attrs inherit the active group prefix")
}

func TestPrettyHandler_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(newPrettyHandler(&buf, slog.LevelWarn, false, false))

	log.Info("infoline")
	log.Warn("warnline")

	out := buf.String()
	assert.NotContains(t, out, "infoline")
	assert.Contains(t, out, "warnline")
}

func TestPrettyHandler_IncludeTime(t *testing.T) {
	var withTime, withoutTime bytes.Buffer
	rec := slog.NewRecord(refTime(), slog.LevelInfo, "hello", 0)

	require.NoError(t, newPrettyHandler(&withTime, slog.LevelInfo, true, false).Handle(context.Background(), rec))
	require.NoError(t, newPrettyHandler(&withoutTime, slog.LevelInfo, false, false).Handle(context.Background(), rec))

	assert.Contains(t, withTime.String(), "2026-06-27 12:30:45", "includeTime renders a full datetime")
	assert.NotContains(t, withoutTime.String(), "12:30:45", "without includeTime there is no timestamp")
}

func TestNewPlainWriter_StripsSGR(t *testing.T) {
	var buf bytes.Buffer
	w := NewPlainWriter(&buf)

	colored := "\x1b[32mINFO\x1b[0m ready\n"
	n, err := w.Write([]byte(colored))
	require.NoError(t, err)
	assert.Equal(t, len(colored), n, "must report all input bytes consumed")
	assert.Equal(t, "INFO ready\n", buf.String())
}

func TestNewPlainWriter_PassesPlainThrough(t *testing.T) {
	var buf bytes.Buffer
	w := NewPlainWriter(&buf)

	plain := "level=INFO msg=hello\n"
	n, err := w.Write([]byte(plain))
	require.NoError(t, err)
	assert.Equal(t, len(plain), n)
	assert.Equal(t, plain, buf.String(), "plain input must pass through byte-for-byte")
}

// refTime is a fixed, non-zero time for deterministic timestamp assertions.
func refTime() time.Time { return time.Date(2026, 6, 27, 12, 30, 45, 0, time.UTC) }

// forceColorProfile pins lipgloss to a colored profile for the duration of a
// test and restores the previous one on cleanup.
func forceColorProfile(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}
