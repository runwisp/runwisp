// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// CopyDialog renders a centered modal that displays selectable text for manual
// copying. Used as a fallback when clipboard access (atotto) is unavailable
// (e.g. SSH without xclip, containers).
type CopyDialog struct {
	title string
	value string
}

func NewCopyDialog(title, value string) CopyDialog {
	return CopyDialog{title: title, value: value}
}

func (d *CopyDialog) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "enter", "backspace", "q":
			return true
		}
	case tea.MouseMsg:
		// Right-click closes the dialog; left-click is reserved for text selection.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonRight {
			return true
		}
	}
	return false
}

func (d *CopyDialog) View(screenWidth, screenHeight int) string {
	valueLen := len([]rune(d.value))
	dialogWidth, innerWidth := modalDimensions(screenWidth, valueLen+8, 44)
	titleStr := modalSurfaceLine(d.title, innerWidth, uikit.ColorTextBright, true)

	// Value rendered as bright text on a slightly distinct background
	// so the user can visually identify and triple-click to select.
	valueBg := uikit.ColorSidebarBg
	valueStr := lipgloss.NewStyle().
		Foreground(uikit.ColorWhite).
		Background(valueBg).
		Bold(true).
		Padding(0, 1).
		Width(innerWidth).
		Align(lipgloss.Center).
		Render(d.value)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorSecondary, []string{
		modalEmptyLine(innerWidth),
		titleStr,
		modalEmptyLine(innerWidth),
		valueStr,
		modalEmptyLine(innerWidth),
		modalSurfaceLine("select text above · esc/enter close", innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	})
	return box.view
}
