// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// releaseNotesURL is the static link shown (and opened) by NewReleaseDialog.
const releaseNotesURL = "https://github.com/runwisp/runwisp/releases"

// NewReleaseDialog is the "more info" modal behind the sidebar update indicator:
// a centered card naming the newer release, the running version, and a link to
// the release notes. Everything but the link is informational; the link is a
// real focusable/clickable control, mirroring ConfirmDialog's button
// hit-testing: tab or click focuses/activates it, enter or click opens it in
// the browser via the same open → clipboard-fallback pipeline as every other
// browser action in the TUI (see openReleaseNotesCmd, handleOpenBrowser). Any
// other key or click dismisses the dialog.
type NewReleaseDialog struct {
	current string
	latest  string

	linkFocused bool
	linkHovered bool

	// Layout info cached during View() for mouse hit detection.
	linkX1, linkX2, linkY int
}

func NewNewReleaseDialog(current, latest string) NewReleaseDialog {
	return NewReleaseDialog{current: current, latest: latest}
}

// Update handles input while the dialog is open. Returns a command to run
// (opening the browser) and whether the dialog should close.
func (d *NewReleaseDialog) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return d.handleKeyMsg(msg.String())
	case tea.MouseMotionMsg:
		d.linkHovered = d.hitLink(msg.X, msg.Y)
		return nil, false
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft && d.hitLink(msg.X, msg.Y) {
			return openReleaseNotesCmd(), true
		}
		return nil, true
	}
	return nil, false
}

func (d *NewReleaseDialog) hitLink(x, y int) bool {
	return y == d.linkY && x >= d.linkX1 && x < d.linkX2
}

func (d *NewReleaseDialog) handleKeyMsg(key string) (tea.Cmd, bool) {
	switch key {
	case "tab", "down", "j":
		d.linkFocused = true
		return nil, false
	case "enter", "space":
		if d.linkFocused {
			return openReleaseNotesCmd(), true
		}
		return nil, true
	}
	return nil, true
}

// openReleaseNotesCmd opens the static release-notes URL, reusing the same
// open-browser → clipboard-fallback pipeline as every other browser action in
// the TUI (see handleOpenBrowser).
func openReleaseNotesCmd() tea.Cmd {
	return func() tea.Msg {
		if !canOpenBrowser() {
			return uikit.OpenBrowserMsg{URL: releaseNotesURL}
		}
		if err := openBrowser(releaseNotesURL); err != nil {
			return uikit.OpenBrowserMsg{URL: releaseNotesURL, Err: err}
		}
		return uikit.OpenBrowserMsg{URL: releaseNotesURL, BrowserOpened: true}
	}
}

func (d *NewReleaseDialog) View(screenWidth, screenHeight int) string {
	dialogWidth, innerWidth := modalDimensions(screenWidth, 50, 40)

	linkFg, linkBg := uikit.ColorTextBright, uikit.ColorBgLight
	if d.linkFocused || d.linkHovered {
		linkFg, linkBg = uikit.ColorBg, uikit.ColorWarning
	}
	linkLine := lipgloss.NewStyle().
		Background(linkBg).
		Foreground(linkFg).
		Bold(d.linkFocused || d.linkHovered).
		Underline(true).
		Width(innerWidth).
		Align(lipgloss.Center).
		Render(releaseNotesURL)

	hint := "tab focus link · press any key to close"
	if d.linkFocused {
		hint = "enter open link · esc close"
	}

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Update available", innerWidth, uikit.ColorWarning, true),
		modalEmptyLine(innerWidth),
		modalSurfaceLine(d.latest+" is available", innerWidth, uikit.ColorTextBright, false),
		modalSurfaceLine("You're on v"+d.current, innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
		linkLine,
		modalEmptyLine(innerWidth),
		modalSurfaceLine(hint, innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	}
	const linkLineIndex = 6

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorWarning, lines)

	// Cache the link's hitbox for mouse hover/click, mirroring ConfirmDialog's
	// button hit-testing.
	linkWidth := lipgloss.Width(releaseNotesURL)
	d.linkY = box.top + 1 + linkLineIndex
	d.linkX1 = box.left + 2 + (innerWidth-linkWidth)/2
	d.linkX2 = d.linkX1 + linkWidth

	return box.view
}
