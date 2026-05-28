// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package clilog

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSlog returns a buffer that the default slog logger will be pointed at
// by a subsequent Configure/SetOutput, restoring the previous logger on cleanup.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &bytes.Buffer{}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"DEBUG":   slog.LevelDebug,
		" warn ":  slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	}
	for in, want := range cases {
		got, err := ParseLevel(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := ParseLevel("loud")
	assert.Error(t, err, "unknown level must be rejected, not silently defaulted")
}

func TestParseFormat(t *testing.T) {
	cases := map[string]Format{
		"":     FormatAuto,
		"auto": FormatAuto,
		"TEXT": FormatText,
		"json": FormatJSON,
	}
	for in, want := range cases {
		got, err := ParseFormat(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := ParseFormat("yaml")
	assert.Error(t, err, "unknown format must be rejected, not silently defaulted")
}

func TestConfigure_DaemonModeIncludesTimestamp(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "") // treated as unset → timestamps kept
	buf := captureSlog(t)
	Configure(Options{Level: slog.LevelInfo, Format: FormatText, Output: buf, DaemonMode: true})

	slog.Info("hello")
	assert.Contains(t, buf.String(), "time=", "daemon mode must timestamp every line")
}

func TestConfigure_JournalStreamSuppressesTimestamp(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "8:123456")
	buf := captureSlog(t)
	Configure(Options{Level: slog.LevelInfo, Format: FormatText, Output: buf, DaemonMode: true})

	slog.Info("hello")
	assert.NotContains(t, buf.String(), "time=", "systemd adds its own timestamps; ours would be redundant")
}

func TestConfigure_CLIAndTUIModeSuppressTimestamp(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "")
	for _, opts := range []Options{
		{Level: slog.LevelInfo, Format: FormatText},                // plain CLI command
		{Level: slog.LevelInfo, Format: FormatText, TUIMode: true}, // TUI debug panel
	} {
		buf := captureSlog(t)
		Configure(Options{Level: opts.Level, Format: opts.Format, Output: buf, TUIMode: opts.TUIMode})
		slog.Info("hello")
		assert.NotContains(t, buf.String(), "time=")
	}
}

func TestConfigure_JSONAlwaysKeepsTime(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "8:123456") // even under systemd, JSON keeps time
	buf := captureSlog(t)
	Configure(Options{Level: slog.LevelInfo, Format: FormatJSON, Output: buf, DaemonMode: true})

	slog.Info("hello", "k", "v")
	out := buf.String()
	assert.Contains(t, out, `"msg":"hello"`)
	assert.Contains(t, out, `"k":"v"`)
	assert.Contains(t, out, `"time":`)
}

func TestConfigure_LevelFiltering(t *testing.T) {
	buf := captureSlog(t)
	Configure(Options{Level: slog.LevelWarn, Format: FormatText, Output: buf, DaemonMode: true})

	slog.Info("infoline")
	slog.Warn("warnline")
	out := buf.String()
	assert.NotContains(t, out, "infoline")
	assert.Contains(t, out, "warnline")
}

func TestSetOutput_RedirectsPreservingConfig(t *testing.T) {
	Configure(Options{Level: slog.LevelInfo, Format: FormatText, DaemonMode: true})
	buf := captureSlog(t)
	SetOutput(buf)

	slog.Info("marker-xyz")
	assert.Contains(t, buf.String(), "marker-xyz")
}

func TestFancyBanner_JSONNeverFancy(t *testing.T) {
	assert.False(t, FancyBanner(FormatJSON), "JSON output is for log pipelines, never a fancy banner")
}
