// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// programMessenger is the subset of *tea.Program that DebugLogWriter needs.
// Tests substitute a fake without spinning up a real Bubble Tea loop.
type programMessenger interface {
	Send(msg tea.Msg)
}

// DebugLogWriter is an io.Writer that forwards each write as a uikit.DebugLogMsg
// to a Bubble Tea program. It is safe for concurrent use.
//
// Before the program is attached via SetProgram, messages are buffered
// (up to 256 entries). Once attached, buffered messages are flushed.
type DebugLogWriter struct {
	mu      sync.Mutex
	program programMessenger
	buf     []uikit.DebugLogMsg
}

const debugWriterBufCap = 256

func NewDebugLogWriter() *DebugLogWriter {
	return &DebugLogWriter{
		buf: make([]uikit.DebugLogMsg, 0, debugWriterBufCap),
	}
}

// SetProgram flushes buffered messages to the Bubble Tea program, then
// attaches it. tea.Program.Send blocks until the program runs, so call this on
// its own goroutine before Run. Holding the lock through the flush keeps lines
// in order: a Write meanwhile waits and lands after the buffered ones.
func (w *DebugLogWriter) SetProgram(p programMessenger) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, msg := range w.buf {
		p.Send(msg)
	}
	w.buf = nil
	w.program = p
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
