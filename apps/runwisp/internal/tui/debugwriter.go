// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// DebugLogWriter is an io.Writer that forwards each write as a uikit.DebugLogMsg
// to a Bubble Tea program. It is safe for concurrent use.
//
// Before the program is attached via SetProgram, messages are buffered
// (up to 256 entries). Once attached, buffered messages are flushed.
type DebugLogWriter struct {
	mu      sync.Mutex
	program *tea.Program
	buf     []uikit.DebugLogMsg
}

const debugWriterBufCap = 256

func NewDebugLogWriter() *DebugLogWriter {
	return &DebugLogWriter{
		buf: make([]uikit.DebugLogMsg, 0, debugWriterBufCap),
	}
}

// SetProgram attaches the Bubble Tea program and flushes buffered messages.
func (w *DebugLogWriter) SetProgram(p *tea.Program) {
	w.mu.Lock()
	w.program = p
	pending := w.buf
	w.buf = nil
	w.mu.Unlock()

	for _, msg := range pending {
		p.Send(msg)
	}
}

// Write implements io.Writer. Each call is split on newlines and forwarded
// as individual uikit.DebugLogMsg messages.
func (w *DebugLogWriter) Write(p []byte) (int, error) {
	raw := strings.TrimRight(string(p), "\r\n")
	if raw == "" {
		return len(p), nil
	}

	lines := strings.Split(raw, "\n")

	w.mu.Lock()
	defer w.mu.Unlock()

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		msg := uikit.DebugLogMsg{Message: line}

		if w.program != nil {
			w.program.Send(msg)
		} else if len(w.buf) < debugWriterBufCap {
			w.buf = append(w.buf, msg)
		}
	}

	return len(p), nil
}
