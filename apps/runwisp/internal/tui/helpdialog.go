// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// HelpDialog renders a centered modal listing all keyboard shortcuts, grouped
// by context. Opened with `?` from anywhere outside text-input overlays. The
// reference table is taller than most terminals, so the content area scrolls.
type HelpDialog struct {
	scroll int

	// viewport and total are cached during View so Update can clamp scrolling
	// without re-deriving the layout. viewport is the number of content rows the
	// modal can show given the current screen height.
	viewport int
	total    int
}

func NewHelpDialog() HelpDialog {
	return HelpDialog{}
}

// Update reports true when the dialog should close.
func (d *HelpDialog) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return d.handleKey(msg.String())
	case tea.MouseMsg:
		return d.handleMouse(msg)
	}
	return false
}

// handleKey applies a keypress, returning true on a close key.
func (d *HelpDialog) handleKey(key string) bool {
	switch key {
	case "?", "esc", "enter", "q":
		return true
	case "up", "k":
		d.scrollBy(-1)
	case "down", "j":
		d.scrollBy(1)
	case "pgup":
		d.scrollBy(-d.viewport)
	case "pgdown":
		d.scrollBy(d.viewport)
	case "g", "home":
		d.scroll = 0
	case "G", "end":
		d.scroll = d.maxScroll()
	}
	return false
}

// handleMouse scrolls on the wheel and closes on any other press.
func (d *HelpDialog) handleMouse(msg tea.MouseMsg) bool {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			d.scrollBy(-1)
		case tea.MouseWheelDown:
			d.scrollBy(1)
		}
	case tea.MouseClickMsg:
		return true
	}
	return false
}

// scrollBy moves the viewport by delta rows, clamped to the scrollable range.
func (d *HelpDialog) scrollBy(delta int) {
	d.scroll += delta
	if d.scroll < 0 {
		d.scroll = 0
	}
	if d.scroll > d.maxScroll() {
		d.scroll = d.maxScroll()
	}
}

func (d *HelpDialog) maxScroll() int {
	ms := d.total - d.viewport
	if ms < 0 {
		return 0
	}
	return ms
}

func (d *HelpDialog) View(screenWidth, screenHeight int) string {
	const keyColWidth = 13
	dialogWidth, innerWidth := modalDimensions(screenWidth, 52, 44)

	content := helpContentLines(innerWidth, keyColWidth)

	// Reserve rows for the chrome that always frames the content: accent bar,
	// blank+title above, blank+hint+blank below. The rest is the viewport.
	const chromeRows = 6
	viewport := screenHeight - chromeRows - 2
	if viewport < 3 {
		viewport = 3
	}
	if viewport > len(content) {
		viewport = len(content)
	}
	d.viewport = viewport
	d.total = len(content)
	if d.scroll > d.maxScroll() {
		d.scroll = d.maxScroll()
	}

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Keyboard Shortcuts", innerWidth, uikit.ColorTextBright, true),
	}
	end := d.scroll + viewport
	if end > len(content) {
		end = len(content)
	}
	lines = append(lines, content[d.scroll:end]...)
	lines = append(lines,
		modalEmptyLine(innerWidth),
		modalSurfaceLine(helpScrollHint(d.maxScroll()), innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorSecondary, lines)
	return box.view
}

// helpContentLines flattens every section into the scrollable body: a blank
// spacer and bold header per section, followed by its key→description rows.
func helpContentLines(innerWidth, keyColWidth int) []string {
	var lines []string
	for _, section := range keys.OverlaySections {
		lines = append(lines,
			modalEmptyLine(innerWidth),
			helpSectionLine(section.Title, innerWidth),
		)
		for _, b := range section.Bindings {
			lines = append(lines, helpEntryLine(b, keyColWidth, innerWidth))
		}
	}
	return lines
}

func helpScrollHint(maxScroll int) string {
	if maxScroll == 0 {
		return "? / esc close"
	}
	return "↑/↓ scroll · ? / esc close"
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
