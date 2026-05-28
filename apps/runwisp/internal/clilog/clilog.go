// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package clilog owns how the RunWisp CLI and daemon write logs: slog handler
// selection (level, text/json, timestamps) and TTY-aware color gating for the
// styled banner. It is deliberately independent of internal/tui so the headless
// daemon path does not borrow its logging behavior from the interactive UI.
package clilog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
)

// Format selects the slog handler shape.
type Format string

const (
	// FormatAuto picks text output (colored when stderr is a TTY).
	FormatAuto Format = "auto"
	// FormatText forces the text handler.
	FormatText Format = "text"
	// FormatJSON forces the JSON handler, for Docker/k8s log pipelines.
	FormatJSON Format = "json"
)

// Options configures the process-wide slog logger and color output.
type Options struct {
	Level  slog.Level
	Format Format
	// Output is the log destination; defaults to os.Stderr when nil.
	Output io.Writer
	// DaemonMode includes RFC3339 timestamps on every line — a long-running
	// daemon needs them (Prime Directive 1 lists timestamps as required) and
	// enables TTY-aware color gating. Short-lived CLI commands leave it false
	// to keep their output clean. Timestamps are still suppressed under systemd
	// (JOURNAL_STREAM), which prepends its own. JSON always keeps time.
	DaemonMode bool
	// TUIMode marks output bound for the TUI debug panel: never timestamped,
	// and the lipgloss color profile is left untouched (the TUI owns the
	// screen and renders its own colors).
	TUIMode bool
}

var (
	mu   sync.Mutex
	base Options

	// stderrTTY and noColor are detected once at process start. The daemon's
	// stderr destination does not change for the life of the process, so a
	// cached snapshot is correct and avoids re-probing on every reconfigure.
	stderrTTY = isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())
	noColor   = os.Getenv("NO_COLOR") != ""
)

// Configure installs the slog default logger from opts. In DaemonMode it also
// gates lipgloss color output (ASCII when stderr is not a TTY, NO_COLOR is set,
// or format is JSON) so the styled banner degrades to plain text instead of
// emitting escape-code garbage into Docker logs / journald. Safe to call
// repeatedly; the last call wins.
func Configure(opts Options) {
	mu.Lock()
	defer mu.Unlock()
	base = opts
	if opts.DaemonMode {
		applyColorProfile(opts.Format)
	}
	applyHandler(opts)
}

// SetOutput redirects log output to w, preserving the level/format/timestamp
// behavior of the last Configure call. Used by the TUI to route logs to its
// debug panel and by tests to capture output.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	base.Output = w
	applyHandler(base)
}

// FancyBanner reports whether the multi-section startup banner should render.
// Headless non-TTY output and JSON format get plain slog summary lines instead,
// keeping Docker/journald logs clean and grep-able.
func FancyBanner(f Format) bool {
	return f != FormatJSON && stderrTTY
}

// ParseLevel maps a user-supplied level string to a slog.Level. Empty defaults
// to info. Unknown values are rejected — no silent fallback.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q (valid: debug, info, warn, error)", s)
}

// ParseFormat maps a user-supplied format string to a Format. Empty defaults to
// auto. Unknown values are rejected — no silent fallback.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(s))) {
	case "", FormatAuto:
		return FormatAuto, nil
	case FormatText:
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	}
	return "", fmt.Errorf("unknown log format %q (valid: auto, text, json)", s)
}

func applyColorProfile(f Format) {
	// Only force ASCII when color is unwanted. When color IS wanted we leave
	// the renderer's auto-detected profile in place rather than pinning one —
	// pinning would downgrade truecolor terminals to 16 colors.
	if noColor || f == FormatJSON || !stderrTTY {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

func applyHandler(opts Options) {
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}
	hopts := &slog.HandlerOptions{Level: opts.Level}
	if !includeTime(opts) {
		hopts.ReplaceAttr = dropTimeAttr
	}
	var h slog.Handler
	if opts.Format == FormatJSON {
		h = slog.NewJSONHandler(out, hopts)
	} else {
		// The text handler already formats time as RFC3339 with millis, so no
		// ReplaceAttr is needed when timestamps are kept.
		h = slog.NewTextHandler(out, hopts)
	}
	slog.SetDefault(slog.New(h))
}

func includeTime(opts Options) bool {
	if opts.Format == FormatJSON {
		return true
	}
	if opts.TUIMode || !opts.DaemonMode {
		return false
	}
	// systemd journal prepends its own timestamps; ours would be redundant.
	return os.Getenv("JOURNAL_STREAM") == ""
}

func dropTimeAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}
