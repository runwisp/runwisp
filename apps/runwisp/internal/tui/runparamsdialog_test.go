// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestRunParamsDialog_NewSortsByKey(t *testing.T) {
	d := NewRunParamsDialog("backup-db", map[string]string{
		"target":   "s3://bucket",
		"compress": "true",
	})
	assert.Equal(t, []string{"compress = true", "target = s3://bucket"}, d.lines)
}

func TestRunParamsDialog_NewEmpty(t *testing.T) {
	d := NewRunParamsDialog("t", nil)
	assert.Empty(t, d.lines)
}

func TestRunParamsDialog_UpdateClosesOnKeys(t *testing.T) {
	closeKeys := map[string]tea.KeyPressMsg{
		"esc":       {Code: tea.KeyEsc},
		"enter":     {Code: tea.KeyEnter},
		"backspace": {Code: tea.KeyBackspace},
		"q":         {Code: 'q', Text: "q"},
	}
	for name, msg := range closeKeys {
		t.Run(name, func(t *testing.T) {
			d := NewRunParamsDialog("t", map[string]string{"k": "v"})
			assert.True(t, d.Update(msg), "expected close on %q", name)
		})
	}
}

func TestRunParamsDialog_UpdateIgnoresOtherKeys(t *testing.T) {
	d := NewRunParamsDialog("t", map[string]string{"k": "v"})
	assert.False(t, d.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}))
}

func TestRunParamsDialog_UpdateRightClickCloses(t *testing.T) {
	d := NewRunParamsDialog("t", map[string]string{"k": "v"})
	assert.True(t, d.Update(tea.MouseClickMsg{Button: tea.MouseRight}))
}

func TestRunParamsDialog_UpdateLeftClickDoesNotClose(t *testing.T) {
	d := NewRunParamsDialog("t", map[string]string{"k": "v"})
	assert.False(t, d.Update(tea.MouseClickMsg{Button: tea.MouseLeft}))
}

func TestRunParamsDialog_ViewContainsTitleAndPairs(t *testing.T) {
	d := NewRunParamsDialog("backup-db", map[string]string{
		"target":   "s3://bucket",
		"compress": "true",
	})
	out := d.View(80, 24)
	for _, want := range []string{"backup-db", "target = s3://bucket", "compress = true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view missing %q:\n%s", want, out)
		}
	}
}
