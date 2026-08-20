// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package uikit holds the shared styles, colours, message types, and helper
// functions used across every TUI view subpackage. It is the lowest-level
// package in the TUI tree — nothing in `internal/tui` may import a view
// subpackage in a way that creates a cycle with this one.
package uikit

import "charm.land/lipgloss/v2"

// Brand palette — official RunWisp colors.
var (
	ColorPrimary       = lipgloss.Color("#526ee3")
	ColorPrimaryDim    = lipgloss.Color("#3d54b5")
	ColorSecondary     = lipgloss.Color("#009371")
	ColorSecondaryDim  = lipgloss.Color("#006e54")
	ColorBg            = lipgloss.Color("#1a1b26")
	ColorBgLight       = lipgloss.Color("#24253a")
	ColorChartBg       = lipgloss.Color("#1f2030")
	ColorSidebarBg     = lipgloss.Color("#1e2140")
	ColorSidebarActive = lipgloss.Color("#2a3060")
	ColorSidebarHover  = lipgloss.Color("#262b50")
	ColorText          = lipgloss.Color("#c0caf5")
	ColorTextMuted     = lipgloss.Color("#565f89")
	ColorTextBright    = lipgloss.Color("#e0e6ff")
	ColorSuccess       = lipgloss.Color("#9ece6a")
	ColorError         = lipgloss.Color("#f7768e")
	ColorWarning       = lipgloss.Color("#e0af68")
	ColorRunning       = lipgloss.Color("#7aa2f7")
	ColorPending       = lipgloss.Color("#bb9af7")
	ColorWhite         = lipgloss.Color("#ffffff")
	ColorTextDim       = lipgloss.Color("#a8b2d8")
)

var (
	SidebarBrandStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorPrimary).
				Bold(true)
	SidebarMarkStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorSecondary).
				Bold(true)

	SidebarVersionStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorTextMuted)

	SidebarFingerprintStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorTextMuted).
				Italic(true)

	// Sidebar item styles — one per visual state.
	// States: none, focused, selected, focused+selected, focused+cursor, focused+cursor+selected.

	// none: sidebar unfocused, not selected, not under cursor.
	SidebarItemNoneStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorTextMuted)

	// focused: sidebar focused, not selected, not under cursor.
	SidebarItemFocusedStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorText)

	// selected: sidebar unfocused, item is the active selection.
	SidebarItemSelectedStyle = lipgloss.NewStyle().
					Background(ColorSidebarActive).
					Foreground(ColorSecondaryDim).
					Bold(true)

	// focused+selected: sidebar focused, item selected, cursor elsewhere.
	SidebarItemFocusedSelectedStyle = lipgloss.NewStyle().
					Background(ColorSidebarActive).
					Foreground(ColorSecondary).
					Bold(true)

	// focused+cursor: sidebar focused, cursor on item, not selected.
	SidebarItemFocusedCursorStyle = lipgloss.NewStyle().
					Background(ColorSidebarActive).
					Foreground(ColorTextBright)

	// focused+cursor+selected: sidebar focused, cursor on item, item is selected.
	SidebarItemFocusedCursorSelectedStyle = lipgloss.NewStyle().
						Background(ColorSidebarActive).
						Foreground(ColorWhite).
						Bold(true)

	// hovered: mouse pointer is over this item (not selected, not cursor).
	SidebarItemHoveredStyle = lipgloss.NewStyle().
				Background(ColorSidebarHover).
				Foreground(ColorTextBright)

	// group header: non-selectable label above a group of tasks.
	SidebarGroupHeaderStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorTextMuted).
				Bold(true)
)

var (
	TableHeaderStyle = lipgloss.NewStyle().
				Background(ColorBgLight).
				Foreground(ColorTextMuted).
				Bold(true)

	TableFooterStyle = lipgloss.NewStyle().
				Background(ColorBgLight).
				Foreground(ColorTextMuted)
)

// ColorExecRowHover is the background colour for the hovered execution row.
var ColorExecRowHover = lipgloss.Color("#1f2038")

// StatusStyle returns the badge style for a given run status string.
func StatusStyle(status string) lipgloss.Style {
	base := lipgloss.NewStyle().
		PaddingLeft(1).
		PaddingRight(1).
		Bold(true)

	switch status {
	case "running":
		return base.Background(ColorRunning).Foreground(ColorBg)
	case "succeeded":
		return base.Background(ColorSuccess).Foreground(ColorBg)
	case "failed":
		return base.Background(ColorError).Foreground(ColorBg)
	case "pending":
		return base.Background(ColorPending).Foreground(ColorBg)
	case "stopped":
		return base.Background(ColorWarning).Foreground(ColorBg)
	case "timeout":
		return base.Background(ColorWarning).Foreground(ColorBg)
	case "crashed":
		return base.Background(ColorError).Foreground(ColorBg)
	default:
		return base.Background(ColorTextMuted).Foreground(ColorBg)
	}
}

// HelpBarStyle styles the bottom help bar.
var HelpBarStyle = lipgloss.NewStyle().
	Background(ColorBgLight).
	Foreground(ColorTextMuted).
	PaddingLeft(1).
	PaddingRight(1)

var (
	BtnRunNowStyle = lipgloss.NewStyle().
			Background(ColorPrimary).
			Foreground(ColorWhite).
			Bold(true).
			Padding(0, 1)

	BtnStopStyle = lipgloss.NewStyle().
			Background(ColorError).
			Foreground(ColorWhite).
			Bold(true).
			Padding(0, 1)

	BtnRetryStyle = lipgloss.NewStyle().
			Background(ColorWarning).
			Foreground(ColorBg).
			Bold(true).
			Padding(0, 1)

	BtnRunNowHoverStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#6b85f0")).
				Foreground(ColorWhite).
				Bold(true).
				Padding(0, 1)

	BtnStopHoverStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#f99aae")).
				Foreground(ColorWhite).
				Bold(true).
				Padding(0, 1)

	BtnRetryHoverStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#ebc580")).
				Foreground(ColorBg).
				Bold(true).
				Padding(0, 1)

	BtnBackStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#2a2d48")).
			Foreground(ColorTextMuted).
			Bold(true).
			Padding(0, 1)

	BtnBackHoverStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#3a3d5c")).
				Foreground(ColorTextBright).
				Bold(true).
				Padding(0, 1)
)

var (
	InfoLabelStyle = lipgloss.NewStyle().
			Background(ColorBg).
			Foreground(ColorTextMuted).
			Width(16)

	InfoValueStyle = lipgloss.NewStyle().
			Background(ColorBg).
			Foreground(ColorText)

	InfoCapsAvailableStyle = lipgloss.NewStyle().
				Background(ColorBg).
				Foreground(ColorSuccess).
				Bold(true)

	InfoCapsUnavailableStyle = lipgloss.NewStyle().
					Background(ColorBg).
					Foreground(ColorTextMuted)

	InfoSectionStyle = lipgloss.NewStyle().
				Background(ColorBg).
				Foreground(ColorTextBright).
				Bold(true).
				PaddingLeft(2)

	InfoStatValueStyle = lipgloss.NewStyle().
				Background(ColorBg).
				Foreground(ColorTextBright).
				Bold(true)

	InfoStatLabelStyle = lipgloss.NewStyle().
				Background(ColorBg).
				Foreground(ColorTextMuted)

	InfoDividerStyle = lipgloss.NewStyle().
				Background(ColorBg).
				Foreground(lipgloss.Color("#2a2d48"))
)
