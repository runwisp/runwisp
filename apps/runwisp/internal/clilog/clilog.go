// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package clilog owns how the RunWisp CLI and daemon write logs: slog handler
// selection (level, text/json, timestamps) and TTY-aware color gating for the
// styled banner. It is deliberately independent of internal/tui so the headless
// daemon path does not borrow its logging behavior from the interactive UI.
package clilog

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	var h slog.Handler
	if opts.Format == FormatJSON {
		// JSON always keeps its own time field, so no ReplaceAttr is needed.
		h = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: opts.Level})
	} else {
		// Both text formats use the human-readable pretty handler; color is the
		// only difference (see useColor). JSON stays machine-shaped for pipelines.
		h = newPrettyHandler(out, opts.Level, includeTime(opts), useColor(opts))
	}
	slog.SetDefault(slog.New(h))
}

// useColor reports whether the pretty handler should emit ANSI color. Color is
// for an interactive terminal only: --log-format=text is the explicit plain
// escape hatch, NO_COLOR / non-TTY force plain, and the TUI owns its own screen.
func useColor(opts Options) bool {
	return opts.Format == FormatAuto && stderrTTY && !noColor && !opts.TUIMode
}

// NewPlainWriter returns an io.Writer that strips ANSI escape sequences before
// delegating to w. It lets the colored daemon stream be mirrored into a plain
// destination — the daemon log ring buffer, which is replayed/streamed over SSE
// to remote TUI clients — without leaking escape codes into it. Writes with no
// escape byte pass straight through, so the no-color path costs nothing.
func NewPlainWriter(w io.Writer) io.Writer {
	return &plainWriter{w: w}
}

type plainWriter struct{ w io.Writer }

func (p *plainWriter) Write(b []byte) (int, error) {
	if !bytes.ContainsRune(b, 0x1b) {
		return p.w.Write(b)
	}
	if _, err := io.WriteString(p.w, ansi.Strip(string(b))); err != nil {
		return 0, err
	}
	return len(b), nil
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
