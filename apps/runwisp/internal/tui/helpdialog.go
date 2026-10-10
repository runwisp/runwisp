// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
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

// Update reports whether the dialog should close.
func (d *HelpDialog) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return nil, d.handleKey(msg.String())
	case tea.MouseMsg:
		return nil, d.handleMouse(msg)
	}
	return nil, false
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
	d.scroll = min(max(d.scroll, 0), d.maxScroll())
}

func (d *HelpDialog) maxScroll() int {
	return max(d.total-d.viewport, 0)
}

func (d *HelpDialog) View(screenWidth, screenHeight int) string {
	keyColWidth := helpKeyColWidth()
	dialogWidth, innerWidth := modalDimensions(screenWidth, 64, 44)

	content := helpContentLines(innerWidth, keyColWidth)

	// Reserve rows for the chrome that always frames the content: accent bar,
	// blank+title above, blank+hint+blank below. The rest is the viewport.
	const chromeRows = 6
	viewport := screenHeight - chromeRows - 2
	viewport = min(max(viewport, 3), len(content))
	d.viewport = viewport
	d.total = len(content)
	d.scroll = min(d.scroll, d.maxScroll())

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Keyboard Shortcuts", innerWidth, uikit.ColorTextBright, true),
	}
	end := min(d.scroll+viewport, len(content))
	lines = append(lines, content[d.scroll:end]...)
	lines = append(lines, modalFooter(scrollHint(d.maxScroll(), "? / esc close"), innerWidth)...)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorSecondary, lines)
	return box.view
}

// helpContentLines flattens every section into the scrollable body: a blank
// spacer and bold header per section, followed by its key→description rows.
func helpContentLines(innerWidth, keyColWidth int) []string {
	var lines []string
	for _, section := range helpSections() {
		lines = append(lines,
			modalEmptyLine(innerWidth),
			modalSectionLine(section.Title, innerWidth),
		)
		for _, b := range section.Bindings {
			lines = append(lines, helpEntryLines(b, keyColWidth, innerWidth)...)
		}
	}
	return lines
}

// helpSections is the key reference plus a legend for the sidebar's task
// status marks, built from the marks themselves so the two can't drift.
func helpSections() []keys.Section {
	legend := keys.Section{Title: "Sidebar marks"}
	for _, mark := range uikit.TaskMarks {
		legend.Bindings = append(legend.Bindings, keys.Binding{Keys: mark.Glyph, Desc: mark.Meaning})
	}
	return append(slices.Clone(keys.OverlaySections), legend)
}

// helpKeyColWidth fits the widest key combo plus its 2-cell indent and a
// 2-cell gap, so no combo ever wraps.
func helpKeyColWidth() int {
	w := 0
	for _, section := range helpSections() {
		for _, b := range section.Bindings {
			w = max(w, uikit.VisibleWidth(b.Keys))
		}
	}
	return w + 4
}

// helpEntryLines renders one "keys → description" entry with a fixed key
// column. A description too long for one row wraps under itself (hanging
// indent), and each wrapped row is its own line so the scroll math counts it.
func helpEntryLines(b keys.Binding, keyColWidth, innerWidth int) []string {
	keyCol := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextBright).
		Width(keyColWidth).
		Render("  " + b.Keys)
	descWidth := max(innerWidth-keyColWidth, 1)
	desc := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextMuted).
		Width(descWidth).
		Render(b.Desc)
	keyCol = lipgloss.NewStyle().Height(lipgloss.Height(desc)).Background(uikit.ColorBgLight).Render(keyCol)
	return strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, keyCol, desc), "\n")
}
