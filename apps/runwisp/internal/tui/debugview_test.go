// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestDebugView_AppendLineIncrementsSeq(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	v.AppendLine("hello")
	v.AppendLine("world")
	assert.Equal(t, int64(2), v.lineSeq)
}

func TestDebugView_KeysAndWheelScroll(t *testing.T) {
	v := NewDebugView()
	v.SetSize(80, 24)
	for range 50 {
		v.AppendLine("line")
	}
	bottom := v.pane.Scroll
	v.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	assert.Equal(t, bottom-1, v.pane.Scroll)
	v.Update(tea.WindowSizeMsg{Width: 80, Height: 24}) // not a key: ignored
	assert.Equal(t, bottom-1, v.pane.Scroll)
	v.ScrollUp(5)
	assert.Equal(t, bottom-6, v.pane.Scroll)
	v.ScrollDown(5)
	assert.Equal(t, bottom-1, v.pane.Scroll)
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
