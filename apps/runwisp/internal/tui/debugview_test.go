// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestDebugView_NewIsEmpty(t *testing.T) {
	v := NewDebugView()
	assert.Equal(t, int64(0), v.lineSeq)
}

func TestDebugView_AppendLineIncrementsSeq(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	v.AppendLine("hello")
	v.AppendLine("world")
	assert.Equal(t, int64(2), v.lineSeq)
}

func TestDebugView_SetSizeNoPanic(t *testing.T) {
	v := NewDebugView()
	v.SetSize(120, 40)
	v.SetSize(0, 0)
}

func TestDebugView_UpdateKeyMsgScrolls(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	for i := 0; i < 20; i++ {
		v.AppendLine("filler")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyUp})
	v.Update(tea.KeyMsg{Type: tea.KeyDown})
	v.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	v.Update(tea.KeyMsg{Type: tea.KeyPgDown})
}

func TestDebugView_UpdateNonKeyMsgIsNoop(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	v.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	v.Update(tea.MouseMsg{})
}

func TestDebugView_ScrollUpDownDoesNotPanic(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	for i := 0; i < 50; i++ {
		v.AppendLine("line")
	}
	v.ScrollUp(5)
	v.ScrollDown(5)
}

func TestDebugView_ViewContainsHeader(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	out := v.View()
	if !strings.Contains(out, "Debug Log") {
		t.Fatalf("View must include 'Debug Log' header, got:\n%s", out)
	}
}

func TestDebugView_ViewEmptyPlaceholder(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	out := v.View()
	if !strings.Contains(out, "Waiting for events") {
		t.Fatalf("empty view must include placeholder, got:\n%s", out)
	}
}

func TestDebugView_ViewWithLinesOmitsPlaceholder(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	v.AppendLine("some event happened")
	out := v.View()
	if strings.Contains(out, "Waiting for events") {
		t.Fatalf("non-empty view must NOT include placeholder, got:\n%s", out)
	}
}
