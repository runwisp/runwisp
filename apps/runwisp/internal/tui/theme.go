// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import "github.com/charmbracelet/lipgloss"

// Brand palette — official RunWisp colors.
var (
	colorPrimary       = lipgloss.Color("#526ee3")
	colorPrimaryDim    = lipgloss.Color("#3d54b5")
	colorSecondary     = lipgloss.Color("#009371")
	colorSecondaryDim  = lipgloss.Color("#006e54")
	colorBg            = lipgloss.Color("#1a1b26")
	colorBgLight       = lipgloss.Color("#24253a")
	colorChartBg       = lipgloss.Color("#1f2030")
	colorSidebarBg     = lipgloss.Color("#1e2140")
	colorSidebarActive = lipgloss.Color("#2a3060")
	colorSidebarHover  = lipgloss.Color("#262b50")
	colorText          = lipgloss.Color("#c0caf5")
	colorTextMuted     = lipgloss.Color("#565f89")
	colorTextBright    = lipgloss.Color("#e0e6ff")
	colorSuccess       = lipgloss.Color("#9ece6a")
	colorError         = lipgloss.Color("#f7768e")
	colorWarning       = lipgloss.Color("#e0af68")
	colorRunning       = lipgloss.Color("#7aa2f7")
	colorPending       = lipgloss.Color("#bb9af7")
	colorWhite         = lipgloss.Color("#ffffff")
	colorTextDim       = lipgloss.Color("#a8b2d8")
)

// Sidebar styles.
var (
	sidebarBrandStyle = lipgloss.NewStyle().
				Background(colorSidebarBg).
				Foreground(colorPrimary).
				Bold(true)
	sidebarMarkStyle = lipgloss.NewStyle().
				Background(colorSidebarBg).
				Foreground(colorSecondary).
				Bold(true)

	sidebarVersionStyle = lipgloss.NewStyle().
				Background(colorSidebarBg).
				Foreground(colorTextMuted)

	sidebarFingerprintStyle = lipgloss.NewStyle().
				Background(colorSidebarBg).
				Foreground(colorTextMuted).
				Italic(true)

	// Sidebar item styles — one per visual state.
	// States: none, focused, selected, focused+selected, focused+cursor, focused+cursor+selected.

	// none: sidebar unfocused, not selected, not under cursor.
	sidebarItemNoneStyle = lipgloss.NewStyle().
				Background(colorSidebarBg).
				Foreground(colorTextMuted)

	// focused: sidebar focused, not selected, not under cursor.
	sidebarItemFocusedStyle = lipgloss.NewStyle().
				Background(colorSidebarBg).
				Foreground(colorText)

	// selected: sidebar unfocused, item is the active selection.
	sidebarItemSelectedStyle = lipgloss.NewStyle().
					Background(colorSidebarActive).
					Foreground(colorSecondaryDim).
					Bold(true)

	// focused+selected: sidebar focused, item selected, cursor elsewhere.
	sidebarItemFocusedSelectedStyle = lipgloss.NewStyle().
					Background(colorSidebarActive).
					Foreground(colorSecondary).
					Bold(true)

	// focused+cursor: sidebar focused, cursor on item, not selected.
	sidebarItemFocusedCursorStyle = lipgloss.NewStyle().
					Background(colorSidebarActive).
					Foreground(colorTextBright)

	// focused+cursor+selected: sidebar focused, cursor on item, item is selected.
	sidebarItemFocusedCursorSelectedStyle = lipgloss.NewStyle().
						Background(colorSidebarActive).
						Foreground(colorWhite).
						Bold(true)

	// hovered: mouse pointer is over this item (not selected, not cursor).
	sidebarItemHoveredStyle = lipgloss.NewStyle().
				Background(colorSidebarHover).
				Foreground(colorTextBright)

	// group header: non-selectable label above a group of tasks.
	sidebarGroupHeaderStyle = lipgloss.NewStyle().
				Background(colorSidebarBg).
				Foreground(colorTextMuted).
				Bold(true)
)

// Main content styles.
var (
	mainHeaderStyle = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorTextBright).
			Bold(true).
			PaddingLeft(2)

	mainSubheaderStyle = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorTextMuted)

	tableHeaderStyle = lipgloss.NewStyle().
				Background(colorBgLight).
				Foreground(colorTextMuted).
				Bold(true)

	tableFooterStyle = lipgloss.NewStyle().
				Background(colorBgLight).
				Foreground(colorTextMuted)
)

// Execution row hover color.
var colorExecRowHover = lipgloss.Color("#1f2038")

// Status badge styles — colored backgrounds for each status.
func statusStyle(status string) lipgloss.Style {
	base := lipgloss.NewStyle().
		PaddingLeft(1).
		PaddingRight(1).
		Bold(true)

	switch status {
	case "running":
		return base.Background(colorRunning).Foreground(colorBg)
	case "success":
		return base.Background(colorSuccess).Foreground(colorBg)
	case "failed":
		return base.Background(colorError).Foreground(colorBg)
	case "pending":
		return base.Background(colorPending).Foreground(colorBg)
	case "stopped":
		return base.Background(colorWarning).Foreground(colorBg)
	case "timeout":
		return base.Background(colorWarning).Foreground(colorBg)
	case "crashed":
		return base.Background(colorError).Foreground(colorBg)
	default:
		return base.Background(colorTextMuted).Foreground(colorBg)
	}
}

// Help bar style.
var helpBarStyle = lipgloss.NewStyle().
	Background(colorBgLight).
	Foreground(colorTextMuted).
	PaddingLeft(1).
	PaddingRight(1)

// Action button styles.
var (
	btnRunNowStyle = lipgloss.NewStyle().
			Background(colorPrimary).
			Foreground(colorWhite).
			Bold(true).
			Padding(0, 1)

	btnStopStyle = lipgloss.NewStyle().
			Background(colorError).
			Foreground(colorWhite).
			Bold(true).
			Padding(0, 1)

	btnRetryStyle = lipgloss.NewStyle().
			Background(colorWarning).
			Foreground(colorBg).
			Bold(true).
			Padding(0, 1)

	// Hover variants — slightly lighter/different background.
	btnRunNowHoverStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#6b85f0")).
				Foreground(colorWhite).
				Bold(true).
				Padding(0, 1)

	btnStopHoverStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#f99aae")).
				Foreground(colorWhite).
				Bold(true).
				Padding(0, 1)

	btnRetryHoverStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#ebc580")).
				Foreground(colorBg).
				Bold(true).
				Padding(0, 1)

	btnBackStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#2a2d48")).
			Foreground(colorTextMuted).
			Bold(true).
			Padding(0, 1)

	btnBackHoverStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#3a3d5c")).
				Foreground(colorTextBright).
				Bold(true).
				Padding(0, 1)
)

// Info view styles.
var (
	infoLabelStyle = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorTextMuted).
			Width(16)

	infoValueStyle = lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorText)

	infoCapsAvailableStyle = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorSuccess).
				Bold(true)

	infoCapsUnavailableStyle = lipgloss.NewStyle().
					Background(colorBg).
					Foreground(colorTextMuted)

	infoSectionStyle = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorTextBright).
				Bold(true).
				PaddingLeft(2)

	infoStatValueStyle = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorTextBright).
				Bold(true)

	infoStatLabelStyle = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(colorTextMuted)

	infoDividerStyle = lipgloss.NewStyle().
				Background(colorBg).
				Foreground(lipgloss.Color("#2a2d48"))
)
