// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// onConfirmCmd is a sentinel tea.Cmd for confirm
var onConfirmCmd = func() tea.Msg { return struct{}{} }
var onDenyCmd = func() tea.Msg { return struct{ deny bool }{true} }

// TestConfirmDialog_HandleKeyMsg_ToggleSelected verifies left/right/tab toggle selected.
func TestConfirmDialog_HandleKeyMsg_ToggleSelected(t *testing.T) {
	d := NewConfirmDialog("title", "message", onConfirmCmd)
	d.selected = 0

	_, closed := d.handleKeyMsg("left")
	assert.False(t, closed)
	assert.Equal(t, 1, d.selected)

	_, closed = d.handleKeyMsg("right")
	assert.False(t, closed)
	assert.Equal(t, 0, d.selected)

	_, closed = d.handleKeyMsg("tab")
	assert.False(t, closed)
	assert.Equal(t, 1, d.selected)
}

// TestConfirmDialog_HandleKeyMsg_Enter_Yes fires onConfirm when selected=0.
func TestConfirmDialog_HandleKeyMsg_Enter_Yes(t *testing.T) {
	var confirmed bool
	confirm := func() tea.Msg { confirmed = true; return nil }
	d := NewConfirmDialog("title", "message", confirm)
	d.selected = 0

	cmd, closed := d.handleKeyMsg("enter")
	assert.True(t, closed)
	assert.NotNil(t, cmd)
	cmd() //nolint:errcheck
	assert.True(t, confirmed)
}

// TestConfirmDialog_HandleKeyMsg_Enter_No_WithDeny fires onDeny when selected=1 and onDeny is set.
func TestConfirmDialog_HandleKeyMsg_Enter_No_WithDeny(t *testing.T) {
	var denied bool
	deny := func() tea.Msg { denied = true; return nil }
	d := NewChoiceDialog("title", "msg", "Yes", "No", onConfirmCmd, deny)
	d.selected = 1

	cmd, closed := d.handleKeyMsg("enter")
	assert.True(t, closed)
	assert.NotNil(t, cmd)
	cmd() //nolint:errcheck
	assert.True(t, denied)
}

// TestConfirmDialog_HandleKeyMsg_Enter_No_NoDeny returns nil cmd, closed=true.
func TestConfirmDialog_HandleKeyMsg_Enter_No_NoDeny(t *testing.T) {
	d := NewConfirmDialog("title", "message", onConfirmCmd) // no onDeny
	d.selected = 1

	cmd, closed := d.handleKeyMsg("enter")
	assert.True(t, closed)
	assert.Nil(t, cmd)
}

// TestConfirmDialog_HandleKeyMsg_Esc closes without action.
func TestConfirmDialog_HandleKeyMsg_Esc(t *testing.T) {
	d := NewConfirmDialog("title", "message", onConfirmCmd)
	cmd, closed := d.handleKeyMsg("esc")
	assert.True(t, closed)
	assert.Nil(t, cmd)
}

// TestConfirmDialog_HandleKeyMsg_Y fires confirm.
func TestConfirmDialog_HandleKeyMsg_Y(t *testing.T) {
	d := NewConfirmDialog("title", "message", onConfirmCmd)
	cmd, closed := d.handleKeyMsg("y")
	assert.True(t, closed)
	assert.NotNil(t, cmd)
}

// TestConfirmDialog_HandleKeyMsg_N_WithDeny fires deny.
func TestConfirmDialog_HandleKeyMsg_N_WithDeny(t *testing.T) {
	d := NewChoiceDialog("t", "m", "Yes", "No", onConfirmCmd, onDenyCmd)
	cmd, closed := d.handleKeyMsg("n")
	assert.True(t, closed)
	assert.NotNil(t, cmd)
}

// TestConfirmDialog_HandleKeyMsg_N_NoDeny returns nil cmd, closed.
func TestConfirmDialog_HandleKeyMsg_N_NoDeny(t *testing.T) {
	d := NewConfirmDialog("title", "message", onConfirmCmd)
	cmd, closed := d.handleKeyMsg("n")
	assert.True(t, closed)
	assert.Nil(t, cmd)
}

// TestConfirmDialog_HandleKeyMsg_Unknown returns not closed.
func TestConfirmDialog_HandleKeyMsg_Unknown(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	_, closed := d.handleKeyMsg("z")
	assert.False(t, closed)
}

// TestConfirmDialog_HandleClick_Yes fires confirm.
func TestConfirmDialog_HandleClick_Yes(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30

	cmd, closed := d.handleClick(15, 5)
	assert.True(t, closed)
	assert.NotNil(t, cmd)
}

// TestConfirmDialog_HandleClick_No_WithDeny fires deny.
func TestConfirmDialog_HandleClick_No_WithDeny(t *testing.T) {
	d := NewChoiceDialog("t", "m", "Yes", "No", onConfirmCmd, onDenyCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30

	cmd, closed := d.handleClick(25, 5)
	assert.True(t, closed)
	assert.NotNil(t, cmd)
}

// TestConfirmDialog_HandleClick_No_NoDeny returns nil cmd.
func TestConfirmDialog_HandleClick_No_NoDeny(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30

	cmd, closed := d.handleClick(25, 5)
	assert.True(t, closed)
	assert.Nil(t, cmd)
}

// TestConfirmDialog_HandleClick_Miss returns not closed.
func TestConfirmDialog_HandleClick_Miss(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30

	_, closed := d.handleClick(5, 10) // wrong row
	assert.False(t, closed)
}

// TestConfirmDialog_UpdateHover_Yes sets hovered=0.
func TestConfirmDialog_UpdateHover_Yes(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30

	d.updateHover(15, 5)
	assert.Equal(t, 0, d.hovered)
}

// TestConfirmDialog_UpdateHover_No sets hovered=1.
func TestConfirmDialog_UpdateHover_No(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30

	d.updateHover(25, 5)
	assert.Equal(t, 1, d.hovered)
}

// TestConfirmDialog_UpdateHover_Miss sets hovered=-1.
func TestConfirmDialog_UpdateHover_Miss(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30
	d.hovered = 0 // ensure it gets reset

	d.updateHover(5, 10) // wrong row
	assert.Equal(t, -1, d.hovered)
}

// TestConfirmDialog_Update_MouseMotion calls updateHover.
func TestConfirmDialog_Update_MouseMotion(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30

	cmd, closed := d.Update(tea.MouseMsg{Action: tea.MouseActionMotion, X: 15, Y: 5})
	assert.Nil(t, cmd)
	assert.False(t, closed)
	assert.Equal(t, 0, d.hovered) // yes button hovered
}

// TestConfirmDialog_Update_MousePress_Left fires confirm on yes button click.
func TestConfirmDialog_Update_MousePress_Left(t *testing.T) {
	d := NewConfirmDialog("t", "m", onConfirmCmd)
	d.btnY = 5
	d.btnYesX1 = 10
	d.btnYesX2 = 20
	d.btnNoX1 = 22
	d.btnNoX2 = 30

	cmd, closed := d.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 15, Y: 5})
	assert.True(t, closed)
	assert.NotNil(t, cmd)
}

// TestConfirmDialog_StartShutdown transitions into shutting-down state.
func TestConfirmDialog_StartShutdown(t *testing.T) {
	d := NewConfirmDialog("title", "message", onConfirmCmd)
	assert.False(t, d.shuttingDown)

	cmd := d.StartShutdown()

	assert.True(t, d.shuttingDown)
	assert.Equal(t, "Shutting Down", d.title)
	assert.Equal(t, "Stopping daemon...", d.message)
	assert.NotNil(t, cmd)
	// Invoking the returned command should produce a SpinnerTickMsg.
	msg := cmd()
	assert.NotNil(t, msg)
}

// TestConfirmDialog_View_Normal renders the normal (non-shutdown) dialog.
func TestConfirmDialog_View_Normal(t *testing.T) {
	d := NewConfirmDialog("title", "Are you sure?", onConfirmCmd)

	view := d.View(80, 24)

	assert.NotEmpty(t, view)
	// The button position fields should be populated after View().
	assert.NotZero(t, d.btnYesX2)
	assert.NotZero(t, d.btnNoX2)
	assert.NotZero(t, d.btnY)
}

// TestConfirmDialog_RenderShuttingDownLines returns the expected line count.
func TestConfirmDialog_RenderShuttingDownLines(t *testing.T) {
	d := NewConfirmDialog("title", "message", onConfirmCmd)
	d.StartShutdown() //nolint:errcheck

	_, innerWidth := modalDimensions(80, 46, 46)
	titleStr := modalSurfaceLine(d.title, innerWidth, uikit.ColorText, true)
	lines := d.renderShuttingDownLines(innerWidth, titleStr)

	assert.Len(t, lines, 7)
}
