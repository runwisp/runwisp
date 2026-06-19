// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// HelpDialog renders a centered modal listing all keyboard shortcuts, grouped
// by context. Opened with `?` from anywhere outside text-input overlays.
type HelpDialog struct{}

func NewHelpDialog() HelpDialog {
	return HelpDialog{}
}

type helpEntry struct {
	keys string
	desc string
}

type helpSection struct {
	title   string
	entries []helpEntry
}

// helpSections mirrors the contextual help-bar strings (model_view.go) so the
// overlay and the bar never disagree about what a key does.
var helpSections = []helpSection{
	{title: "Global", entries: []helpEntry{
		{"?", "toggle this help"},
		{"q / ctrl+c", "quit"},
		{"n", "notifications panel (Home)"},
		{"/", "search logs of the focused task"},
	}},
	{title: "Navigate", entries: []helpEntry{
		{"↑↓ / kj", "move selection"},
		{"←→ / hl", "switch sidebar ↔ main panel"},
		{"enter", "open / activate / copy field"},
		{"esc / ⌫", "back"},
	}},
	{title: "Task", entries: []helpEntry{
		{"r", "run now (task) · restart (service)"},
		{"enter", "open the selected run"},
	}},
	{title: "Exec view", entries: []helpEntry{
		{"s", "stop run / service"},
		{"r", "retry · restart"},
		{"d / D", "download log / delete run"},
		{"f", "fullscreen logs"},
		{"g / G", "jump to top / end"},
		{"pgup/pgdn", "page through logs"},
	}},
	{title: "Run dialog", entries: []helpEntry{
		{"space / ←→", "toggle a flag on/off"},
		{"←→ / hl", "choose an option"},
		{"ctrl+t", "include empty / omit value"},
		{"enter", "run · esc cancel"},
	}},
	{title: "Notifications", entries: []helpEntry{
		{"enter", "open the run"},
		{"r", "mark read"},
		{"n / esc", "collapse"},
	}},
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
	for _, section := range helpSections {
		lines = append(lines,
			modalEmptyLine(innerWidth),
			helpSectionLine(section.title, innerWidth),
		)
		for _, e := range section.entries {
			lines = append(lines, helpEntryLine(e, keyColWidth, innerWidth))
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
func helpEntryLine(e helpEntry, keyColWidth, innerWidth int) string {
	keys := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextBright).
		Width(keyColWidth).
		Render("  " + e.keys)
	descWidth := max(innerWidth-keyColWidth, 1)
	desc := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted).
		Width(descWidth).
		Render(e.desc)
	return keys + desc
}
