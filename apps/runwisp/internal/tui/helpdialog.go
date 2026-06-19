// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// HelpDialog renders a centered modal listing all keyboard shortcuts, grouped
// by context. Opened with `?` from anywhere outside text-input overlays.
type HelpDialog struct{}

func NewHelpDialog() HelpDialog {
	return HelpDialog{}
}

// Update reports true when the dialog should close.
func (d *HelpDialog) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "?", "esc", "enter", "q":
			return true
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			return true
		}
	}
	return false
}

func (d *HelpDialog) View(screenWidth, screenHeight int) string {
	const keyColWidth = 13
	dialogWidth, innerWidth := modalDimensions(screenWidth, 52, 44)

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Keyboard Shortcuts", innerWidth, uikit.ColorTextBright, true),
	}
	for _, section := range keys.OverlaySections {
		lines = append(lines,
			modalEmptyLine(innerWidth),
			helpSectionLine(section.Title, innerWidth),
		)
		for _, b := range section.Bindings {
			lines = append(lines, helpEntryLine(b, keyColWidth, innerWidth))
		}
	}
	lines = append(lines,
		modalEmptyLine(innerWidth),
		modalSurfaceLine("? / esc close", innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorSecondary, lines)
	return box.view
}

// helpSectionLine renders a left-aligned bold section header.
func helpSectionLine(title string, innerWidth int) string {
	return lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorSecondary).
		Bold(true).
		Width(innerWidth).
		Render(title)
}

// helpEntryLine renders one "keys → description" row with a fixed key column.
func helpEntryLine(b keys.Binding, keyColWidth, innerWidth int) string {
	keyCol := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextBright).
		Width(keyColWidth).
		Render("  " + b.Keys)
	descWidth := max(innerWidth-keyColWidth, 1)
	desc := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted).
		Width(descWidth).
		Render(b.Desc)
	return keyCol + desc
}
