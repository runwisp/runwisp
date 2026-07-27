// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMessenger records every tea.Msg that DebugLogWriter routes to it.
type fakeMessenger struct {
	mu       sync.Mutex
	received []tea.Msg
}

func (f *fakeMessenger) Send(msg tea.Msg) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.received = append(f.received, msg)
}

func (f *fakeMessenger) snapshot() []tea.Msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tea.Msg, len(f.received))
	copy(out, f.received)
	return out
}

// receivedMessages returns the messages buffered by w that have not yet been
// drained to a messenger. It is a behavioural probe (does writing buffer or
// forward?) — not a structural check.
func bufferedMessages(w *DebugLogWriter) []uikit.DebugLogMsg {
	f := &fakeMessenger{}
	w.attach(f)
	out := make([]uikit.DebugLogMsg, 0, len(f.received))
	for _, m := range f.snapshot() {
		if dm, ok := m.(uikit.DebugLogMsg); ok {
			out = append(out, dm)
		}
	}
	return out
}

func TestDebugLogWriter_Write_BuffersWhenNoProgramSet(t *testing.T) {
	w := NewDebugLogWriter()
	n, err := w.Write([]byte("hello world\n"))
	require.NoError(t, err)
	assert.Equal(t, len("hello world\n"), n)

	buffered := bufferedMessages(w)
	require.Len(t, buffered, 1)
	assert.Equal(t, "hello world", buffered[0].Message)
}

func TestDebugLogWriter_Write_EmptyInputBuffersNothing(t *testing.T) {
	w := NewDebugLogWriter()
	_, err := w.Write([]byte("\n"))
	require.NoError(t, err)
	assert.Empty(t, bufferedMessages(w))
}

func TestDebugLogWriter_Write_MultipleLinesBuffered(t *testing.T) {
	w := NewDebugLogWriter()
	_, _ = w.Write([]byte("line1\nline2\nline3"))

	buffered := bufferedMessages(w)
	require.Len(t, buffered, 3)
	assert.Equal(t, "line1", buffered[0].Message)
	assert.Equal(t, "line2", buffered[1].Message)
	assert.Equal(t, "line3", buffered[2].Message)
}

func TestDebugLogWriter_Write_BufferCapNotExceeded(t *testing.T) {
	w := NewDebugLogWriter()
	line := strings.Repeat("x", 50)
	for i := 0; i < debugWriterBufCap+10; i++ {
		_, _ = w.Write([]byte(line + "\n"))
	}
	assert.Len(t, bufferedMessages(w), debugWriterBufCap)
}

func TestDebugLogWriter_SetProgram_FlushesBufferAndForwardsLater(t *testing.T) {
	w := NewDebugLogWriter()
	_, _ = w.Write([]byte("buffered-before-attach\n"))

	f := &fakeMessenger{}
	w.attach(f)

	// Flush sent the pre-attach line.
	flushed := f.snapshot()
	require.Len(t, flushed, 1)
	require.IsType(t, uikit.DebugLogMsg{}, flushed[0])
	assert.Equal(t, "buffered-before-attach", flushed[0].(uikit.DebugLogMsg).Message)

	// After attach, new writes go straight to the messenger — never to the
	// buffer — so a second attach with a fresh messenger sees no carry-over.
	_, _ = w.Write([]byte("post-attach\n"))
	all := f.snapshot()
	require.Len(t, all, 2)
	assert.Equal(t, "post-attach", all[1].(uikit.DebugLogMsg).Message)

	// Confirm the internal buffer is empty by re-attaching to a fresh fake.
	f2 := &fakeMessenger{}
	w.attach(f2)
	assert.Empty(t, f2.snapshot(), "no messages should remain buffered after first flush")
}

// --- DebugView ---

func TestDebugView_Update_NonKeyMsg(t *testing.T) {
	dv := NewDebugView()
	dv.SetSize(80, 20)
	assert.NotPanics(t, func() {
		dv.Update("notakeymsg")
	})
}

func TestDebugView_Update_KeyMsg(t *testing.T) {
	dv := NewDebugView()
	dv.SetSize(80, 20)
	dv.AppendLine("line1")
	assert.NotPanics(t, func() {
		dv.Update(tea.KeyMsg{Type: tea.KeyUp})
	})
}

func TestDebugView_View_Empty(t *testing.T) {
	dv := NewDebugView()
	dv.SetSize(80, 20)
	assert.NotEmpty(t, dv.View())
}

func TestDebugView_View_WithLines(t *testing.T) {
	dv := NewDebugView()
	dv.SetSize(80, 20)
	dv.AppendLine("hello debug")
	assert.Contains(t, dv.View(), "Debug Log")
}
