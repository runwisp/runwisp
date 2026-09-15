// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

// TestNewReleaseDialog_HandleKeyMsg_TabFocusesLink covers tab/down/j moving
// focus onto the link without closing the dialog.
func TestNewReleaseDialog_HandleKeyMsg_TabFocusesLink(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")

	cmd, closed := d.handleKeyMsg("tab")
	assert.False(t, closed)
	assert.Nil(t, cmd)
	assert.True(t, d.linkFocused)
}

// TestNewReleaseDialog_HandleKeyMsg_EnterUnfocused closes without a command —
// the link isn't the default/starter focus.
func TestNewReleaseDialog_HandleKeyMsg_EnterUnfocused(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")

	cmd, closed := d.handleKeyMsg("enter")
	assert.True(t, closed)
	assert.Nil(t, cmd)
}

// TestNewReleaseDialog_HandleKeyMsg_EnterFocused opens the link and closes.
func TestNewReleaseDialog_HandleKeyMsg_EnterFocused(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")
	d.linkFocused = true

	cmd, closed := d.handleKeyMsg("enter")
	assert.True(t, closed)
	assert.NotNil(t, cmd)
}

// TestNewReleaseDialog_HandleKeyMsg_Unknown dismisses like the old
// any-key-closes behavior.
func TestNewReleaseDialog_HandleKeyMsg_Unknown(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")

	cmd, closed := d.handleKeyMsg("x")
	assert.True(t, closed)
	assert.Nil(t, cmd)
}

// TestNewReleaseDialog_HitLink_ClickOpensAndCloses covers a click landing on
// the cached link hitbox.
func TestNewReleaseDialog_HitLink_ClickOpensAndCloses(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")
	d.linkY = 5
	d.linkX1 = 10
	d.linkX2 = 20

	cmd, closed := d.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 15, Y: 5})
	assert.True(t, closed)
	assert.NotNil(t, cmd)
}

// TestNewReleaseDialog_HitLink_ClickMissClosesWithoutCommand mirrors the old
// "any other click dismisses" behavior.
func TestNewReleaseDialog_HitLink_ClickMissClosesWithoutCommand(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")
	d.linkY = 5
	d.linkX1 = 10
	d.linkX2 = 20

	cmd, closed := d.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: 30, Y: 5})
	assert.True(t, closed)
	assert.Nil(t, cmd)
}

// TestNewReleaseDialog_MouseMotion_HoversLink sets linkHovered on the cached hitbox.
func TestNewReleaseDialog_MouseMotion_HoversLink(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")
	d.linkY = 5
	d.linkX1 = 10
	d.linkX2 = 20

	cmd, closed := d.Update(tea.MouseMotionMsg{X: 15, Y: 5})
	assert.Nil(t, cmd)
	assert.False(t, closed)
	assert.True(t, d.linkHovered)
}

// TestNewReleaseDialog_View_PopulatesLinkHitbox exercises View() and checks
// the cached hitbox fields are set to a plausible on-screen position.
func TestNewReleaseDialog_View_PopulatesLinkHitbox(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")

	view := d.View(80, 24)

	assert.NotEmpty(t, view)
	assert.NotZero(t, d.linkY)
	assert.Greater(t, d.linkX2, d.linkX1)
}

// TestNewReleaseDialog_Update_CtrlCIgnored is a control case: NewReleaseDialog
// itself doesn't special-case ctrl+c (the model layer does), so it should just
// close like any other key.
func TestNewReleaseDialog_Update_CtrlCIgnored(t *testing.T) {
	d := NewNewReleaseDialog("1.0.0", "v2.0.0")
	cmd, closed := d.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	assert.True(t, closed)
	assert.Nil(t, cmd)
}
