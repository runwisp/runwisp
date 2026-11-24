// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// WarningsDialog lists the live config's warnings in full, opened from the
// Home header's warning notice (click, or `w`). A crontab can produce more
// warnings than fit on screen, so the body scrolls like the key reference.
type WarningsDialog struct {
	warnings []string
	scroll   modalScroll
}

// Update reports whether the dialog should close: on a close key or a click.
func (d *WarningsDialog) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "enter", "q", "w":
			return nil, true
		}
		d.scroll.handleKey(msg.String())
	case tea.MouseWheelMsg:
		d.scroll.handleWheel(msg)
	case tea.MouseClickMsg:
		return nil, true
	}
	return nil, false
}

func (d *WarningsDialog) View(screenWidth, screenHeight int) string {
	dialogWidth, innerWidth := modalDimensions(screenWidth, 80, 44)
	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Config warnings", innerWidth, uikit.ColorWarning, true),
	}
	lines = append(lines, d.scroll.window(warningLines(d.warnings, innerWidth), modalBodyRows(screenHeight))...)
	lines = append(lines, modalFooter(d.scroll.hint("esc close"), innerWidth)...)
	return renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorWarning, lines).view
}

// warningLines wraps each warning under a ⚠ bullet with a hanging indent,
// one string per row so the scroll math counts wrapped rows.
func warningLines(warnings []string, innerWidth int) []string {
	text := uikit.OnBg(uikit.ColorBgLight, uikit.ColorText).Width(max(innerWidth-2, 1))
	bullet := uikit.OnBg(uikit.ColorBgLight, uikit.ColorWarning)
	pad := uikit.OnBg(uikit.ColorBgLight, uikit.ColorText)
	var lines []string
	for _, warn := range warnings {
		lines = append(lines, modalEmptyLine(innerWidth))
		for i, row := range strings.Split(text.Render(warn), "\n") {
			lead := pad.Render("  ")
			if i == 0 {
				lead = bullet.Render("⚠ ")
			}
			lines = append(lines, lead+row)
		}
	}
	return lines
}
