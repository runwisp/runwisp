// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package uikit holds the shared styles, colours, message types, and helper
// functions used across every TUI view subpackage. It is the lowest-level
// package in the TUI tree — nothing in `internal/tui` may import a view
// subpackage in a way that creates a cycle with this one.
package uikit

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

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

	// ColorButtonRaised is a secondary button's resting fill (Back, an
	// unselected dialog choice); ColorButtonRaisedHover is its hover fill.
	ColorButtonRaised      = lipgloss.Color("#2a2d48")
	ColorButtonRaisedHover = lipgloss.Color("#3a3d5c")

	ColorPrimaryHover = lipgloss.Color("#6b85f0")
	ColorErrorHover   = lipgloss.Color("#f99aae")
	ColorWarningHover = lipgloss.Color("#ebc580")
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

	// none: sidebar unfocused, not selected, not under cursor. Dimmer than
	// focused, but still brighter than the group headers so the hierarchy
	// doesn't flip when focus moves to the main panel.
	SidebarItemNoneStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorTextDim)

	// focused: sidebar focused, not selected, not under cursor.
	SidebarItemFocusedStyle = lipgloss.NewStyle().
				Background(ColorSidebarBg).
				Foreground(ColorText)

	// selected: sidebar unfocused, item is the active selection. Bright text
	// keeps it readable on the active band; the dim green it used to wear
	// nearly vanished against it.
	SidebarItemSelectedStyle = lipgloss.NewStyle().
					Background(ColorSidebarActive).
					Foreground(ColorTextBright).
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

// StatusColor maps a run's display status (phase or end reason) to its
// accent colour. Every failure reason takes the error colour.
func StatusColor(status string) color.Color {
	switch status {
	case string(model.PhaseRunning):
		return ColorRunning
	case string(model.ReasonSuccess):
		return ColorSuccess
	case string(model.PhasePending):
		return ColorPending
	case string(model.ReasonFailed), string(model.ReasonCrashed), string(model.ReasonTimeout),
		string(model.ReasonStartFailed), string(model.ReasonLogOverflow), string(model.ReasonUnhealthy):
		return ColorError
	case string(model.ReasonStopped), string(model.ReasonMissed):
		return ColorWarning
	default:
		return ColorTextMuted
	}
}

// StatusLabel is the word the TUI shows for a run's display status. The long
// end reasons get shorter words so the run table's status badge stays narrow;
// colours still key off the raw status (StatusColor).
func StatusLabel(status string) string {
	switch status {
	case string(model.ReasonSuccess):
		return "success"
	case string(model.ReasonLogOverflow):
		return "log full"
	case string(model.ReasonStartFailed):
		return "start fail"
	case string(model.ReasonQueueFull):
		return "queue full"
	case string(model.ReasonDSTSkipped):
		return "dst skip"
	case string(model.ReasonDaemonStopped):
		return "shutdown"
	default:
		return status
	}
}

// TaskMark is the one-cell status mark at the right edge of a sidebar task
// row. The zero value means no mark.
type TaskMark struct {
	Glyph   string
	Color   color.Color
	Meaning string
}

// Sidebar task marks, in precedence order (a running task that last failed
// shows as running), matching the web UI overview's task states.
var (
	MarkRunning = TaskMark{Glyph: "●", Color: ColorRunning, Meaning: "task is running"}
	MarkFailed  = TaskMark{Glyph: "✗", Color: ColorError, Meaning: "last run failed"}
	MarkStopped = TaskMark{Glyph: "■", Color: ColorTextMuted, Meaning: "service is stopped"}
	MarkPaused  = TaskMark{Glyph: "‖", Color: ColorWarning, Meaning: "schedule is paused"}

	TaskMarks = []TaskMark{MarkRunning, MarkFailed, MarkStopped, MarkPaused}
)

// StatusStyle returns the badge style for a run's display status.
func StatusStyle(status string) lipgloss.Style {
	return lipgloss.NewStyle().
		PaddingLeft(1).
		PaddingRight(1).
		Bold(true).
		Background(StatusColor(status)).
		Foreground(ColorBg)
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
				Background(ColorPrimaryHover).
				Foreground(ColorWhite).
				Bold(true).
				Padding(0, 1)

	BtnStopHoverStyle = lipgloss.NewStyle().
				Background(ColorErrorHover).
				Foreground(ColorWhite).
				Bold(true).
				Padding(0, 1)

	BtnRetryHoverStyle = lipgloss.NewStyle().
				Background(ColorWarningHover).
				Foreground(ColorBg).
				Bold(true).
				Padding(0, 1)

	BtnBackStyle = lipgloss.NewStyle().
			Background(ColorButtonRaised).
			Foreground(ColorTextMuted).
			Bold(true).
			Padding(0, 1)

	BtnBackHoverStyle = lipgloss.NewStyle().
				Background(ColorButtonRaisedHover).
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
				Foreground(ColorButtonRaised)
)
