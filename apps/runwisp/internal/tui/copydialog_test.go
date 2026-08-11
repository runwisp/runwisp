// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestCopyDialog_NewStoresTitleAndValue(t *testing.T) {
	d := NewCopyDialog("Password", "hunter2")
	if d.title != "Password" {
		t.Fatalf("title = %q", d.title)
	}
	if d.value != "hunter2" {
		t.Fatalf("value = %q", d.value)
	}
}

func TestCopyDialog_UpdateClosesOnKeys(t *testing.T) {
	closeKeys := []string{"esc", "enter", "backspace", "q"}
	for _, k := range closeKeys {
		t.Run(k, func(t *testing.T) {
			d := NewCopyDialog("t", "v")
			var msg tea.KeyPressMsg
			switch k {
			case "esc":
				msg = tea.KeyPressMsg{Code: tea.KeyEsc}
			case "enter":
				msg = tea.KeyPressMsg{Code: tea.KeyEnter}
			case "backspace":
				msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
			case "q":
				msg = tea.KeyPressMsg{Code: 'q', Text: "q"}
			}
			assert.True(t, d.Update(msg), "expected close on %q", k)
		})
	}
}

func TestCopyDialog_UpdateIgnoresOtherKeys(t *testing.T) {
	d := NewCopyDialog("t", "v")
	assert.False(t, d.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}))
}

func TestCopyDialog_UpdateRightClickCloses(t *testing.T) {
	d := NewCopyDialog("t", "v")
	closed := d.Update(tea.MouseClickMsg{Button: tea.MouseRight})
	assert.True(t, closed)
}

func TestCopyDialog_UpdateLeftClickDoesNotClose(t *testing.T) {
	d := NewCopyDialog("t", "v")
	closed := d.Update(tea.MouseClickMsg{Button: tea.MouseLeft})
	assert.False(t, closed)
}

func TestCopyDialog_UpdateMouseRelease_DoesNotClose(t *testing.T) {
	d := NewCopyDialog("t", "v")
	closed := d.Update(tea.MouseReleaseMsg{Button: tea.MouseRight})
	assert.False(t, closed)
}

func TestCopyDialog_UpdateNonKeyOrMouseMsgIsIgnored(t *testing.T) {
	d := NewCopyDialog("t", "v")
	closed := d.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assert.False(t, closed)
}

func TestCopyDialog_ViewContainsTitleAndValue(t *testing.T) {
	d := NewCopyDialog("RunWisp Password", "p@ss-123")
	out := d.View(80, 24)
	if !strings.Contains(out, "RunWisp Password") {
		t.Fatalf("view missing title: %s", out)
	}
	if !strings.Contains(out, "p@ss-123") {
		t.Fatalf("view missing value: %s", out)
	}
}
