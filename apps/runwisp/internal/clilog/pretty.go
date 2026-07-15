// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package clilog

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// Palette mirrors internal/tui so the live log stream and the startup banner
// read as one product. clilog stays independent of internal/tui (see the
// package doc), so the few shared colors are redeclared here rather than imported.
var (
	prettyDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")) // dim gray
	prettyMsg   = lipgloss.NewStyle().Bold(true)
	prettyDebug = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")) // dim gray
	prettyInfo  = lipgloss.NewStyle().Foreground(lipgloss.Color("#009371")) // green
	prettyWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24")) // yellow
	prettyError = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")) // red
)

// prettyHandler is a slog.Handler that renders a human-readable line:
//
//	2026-06-27 14:53:41 [INFO] ready, listening url=http://127.0.0.1:9477
//
// It backs both text formats — colored on an interactive terminal (see
// useColor), plain everywhere else — replacing Go's terse logfmt default. JSON
// stays untouched for log pipelines. Color is applied via lipgloss, which
// resolves the global color profile at render time, so the same gating that
// drives the banner (NO_COLOR / non-TTY → ASCII) also plays it safe here.
type prettyHandler struct {
	w  io.Writer
	mu *sync.Mutex // shared across WithAttrs/WithGroup clones; serializes writes

	level       slog.Leveler
	includeTime bool
	color       bool

	groupPrefix  string // accumulated "a.b." prefix from WithGroup
	preformatted string // attrs accrued via WithAttrs, already rendered
}

// newPrettyHandler builds a pretty handler. color is explicit (rather than read
// from the package-level stderrTTY) so the formatter can be unit-tested with
// color forced on or off.
func newPrettyHandler(w io.Writer, level slog.Leveler, includeTime, color bool) *prettyHandler {
	return &prettyHandler{w: w, mu: &sync.Mutex{}, level: level, includeTime: includeTime, color: color}
}

func (h *prettyHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder

	if h.includeTime && !r.Time.IsZero() {
		b.WriteString(h.paint(prettyDim, r.Time.Format("2006-01-02 15:04:05")))
		b.WriteByte(' ')
	}

	b.WriteString(h.levelLabel(r.Level))
	b.WriteByte(' ')
	b.WriteString(h.paint(prettyMsg, r.Message))

	b.WriteString(h.preformatted)
	r.Attrs(func(a slog.Attr) bool {
		h.appendAttr(&b, h.groupPrefix, a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	nh := h.clone()
	var b strings.Builder
	for _, a := range attrs {
		nh.appendAttr(&b, h.groupPrefix, a)
	}
	nh.preformatted = h.preformatted + b.String()
	return nh
}

func (h *prettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := h.clone()
	nh.groupPrefix = h.groupPrefix + name + "."
	return nh
}

func (h *prettyHandler) clone() *prettyHandler {
	c := *h // shares w and mu by value (mu is a pointer)
	return &c
}

// levelLabel renders a bracketed, colored level tag.
func (h *prettyHandler) levelLabel(l slog.Level) string {
	label, st := "[INFO]", prettyInfo
	switch {
	case l < slog.LevelInfo:
		label, st = "[DEBUG]", prettyDebug
	case l >= slog.LevelError:
		label, st = "[ERROR]", prettyError
	case l >= slog.LevelWarn:
		label, st = "[WARN]", prettyWarn
	}
	return h.paint(st, label)
}

// appendAttr writes " key=value", flattening groups into dotted keys. Keys are
// dimmed; values keep the terminal's default color and are logfmt-quoted.
func (h *prettyHandler) appendAttr(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		if len(group) == 0 {
			return
		}
		np := prefix
		if a.Key != "" {
			np = prefix + a.Key + "."
		}
		for _, ga := range group {
			h.appendAttr(b, np, ga)
		}
		return
	}
	b.WriteByte(' ')
	b.WriteString(h.paint(prettyDim, prefix+a.Key+"="))
	b.WriteString(quoteIfNeeded(a.Value.String()))
}

// paint applies a lipgloss style only when color is on; otherwise returns the
// raw string so the plain branch (and unit tests) stay escape-free.
func (h *prettyHandler) paint(st lipgloss.Style, s string) string {
	if !h.color {
		return s
	}
	return st.Render(s)
}

// quoteIfNeeded matches slog's TextHandler quoting: bare tokens stay bare, but
// anything with whitespace, '=', quotes, or control bytes gets Go-quoted.
func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " =\"\n\t\r") {
		return strconv.Quote(s)
	}
	return s
}
