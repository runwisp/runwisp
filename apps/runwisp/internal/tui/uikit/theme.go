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

// Brand palette — official RunWisp colors, the web UI's dark theme
// (packages/ui/src/lib/theme-tokens.css, `:root.dark`) ported to the
// terminal.
//
// This block is the single source of truth for every colour RunWisp paints in
// a terminal: the TUI, and the CLI (fang help, errors, the startup banner, log
// lines, the import summary). Keep it that way: never declare a colour
// anywhere else, reference one of these. A package that can't import uikit
// (clilog) takes its colours as a parameter from a caller that can
// (cmd/runwisp/theme.go). TestNoColorsOutsidePalette fails on a hex colour
// declared outside this file. Add a shade here only when no existing one
// reads right.
var (
	ColorPrimary      = lipgloss.Color("#2fd4dd") // --rw-primary
	ColorPrimaryHover = lipgloss.Color("#5fe3ea") // --rw-primary-hover
	ColorPrimaryDim   = lipgloss.Color("#0e7c86") // logo gradient end
	ColorSecondary    = lipgloss.Color("#15a0a8") // logo gradient start
	ColorBg           = lipgloss.Color("#0c1719") // --rw-surface
	ColorBgLight      = lipgloss.Color("#16292b") // brand ink
	// ColorBgRaised is the one surface a step above ColorBg: the sidebar, a
	// hovered row in the main panel, and the info view's chart well.
	ColorBgRaised      = lipgloss.Color("#122226") // --rw-surface-raised
	ColorSidebarActive = lipgloss.Color("#123438") // --rw-primary-soft over ColorBgRaised
	ColorSidebarHover  = lipgloss.Color("#132c30") // half of ColorSidebarActive's tint
	ColorText          = lipgloss.Color("#c3cfda") // --rw-con-text
	ColorTextMuted     = lipgloss.Color("#6c7d8c") // --rw-con-dim
	ColorTextBright    = lipgloss.Color("#e8eff4") // --rw-on-surface
	ColorSuccess       = lipgloss.Color("#3fc26b") // --rw-success
	ColorError         = lipgloss.Color("#f15a4d") // --rw-danger
	ColorWarning       = lipgloss.Color("#e0a52e") // --rw-warning
	ColorRunning       = lipgloss.Color("#2fd4dd") // --rw-info
	ColorPending       = lipgloss.Color("#be7ace") // --rw-ansi-magenta
	ColorTextDim       = lipgloss.Color("#94a6b1") // --rw-on-surface-muted

	// ColorButtonRaised is a secondary button's resting fill (Back, an
	// unselected dialog choice); ColorButtonRaisedHover is its hover fill.
	ColorButtonRaised      = lipgloss.Color("#2a3a45") // --rw-outline-hover
	ColorButtonRaisedHover = lipgloss.Color("#3a4f5c") // one step lighter

	ColorErrorHover   = lipgloss.Color("#ff7568") // --rw-danger-hover
	ColorWarningHover = lipgloss.Color("#ecb845") // --rw-warning-hover

	// Terminal shades, for the CLI only. CLI output paints no background, so
	// it lands on the terminal's own, light or dark, and the bright accents
	// above wash out on white. These are the web UI's light-theme tokens:
	// mid-tones at 3:1 or better on both white and near-black, so the CLI
	// needs no background probe. The CLI's accent and dim text are
	// ColorSecondary and ColorTextMuted, which already clear that bar. TUI
	// views must not use these (palette_test leaves them out).
	ColorTermSuccess = lipgloss.Color("#1e8a4a") // --rw-success (light)
	ColorTermWarning = lipgloss.Color("#c4781a") // --rw-warning (light)
	ColorTermDanger  = lipgloss.Color("#d6453d") // --rw-danger (light)
)

var (
	SidebarBrandStyle = lipgloss.NewStyle().
				Background(ColorBgRaised).
				Foreground(ColorPrimary).
				Bold(true)
	SidebarMarkStyle = lipgloss.NewStyle().
				Background(ColorBgRaised).
				Foreground(ColorSecondary).
				Bold(true)

	SidebarVersionStyle = lipgloss.NewStyle().
				Background(ColorBgRaised).
				Foreground(ColorTextMuted)

	SidebarFingerprintStyle = lipgloss.NewStyle().
				Background(ColorBgRaised).
				Foreground(ColorTextMuted).
				Italic(true)

	// Sidebar item styles — one per visual state.
	// States: none, focused, selected, focused+selected, focused+cursor, focused+cursor+selected.

	// none: sidebar unfocused, not selected, not under cursor. Dimmer than
	// focused, but still brighter than the group headers so the hierarchy
	// doesn't flip when focus moves to the main panel.
	SidebarItemNoneStyle = lipgloss.NewStyle().
				Background(ColorBgRaised).
				Foreground(ColorTextDim)

	// focused: sidebar focused, not selected, not under cursor.
	SidebarItemFocusedStyle = lipgloss.NewStyle().
				Background(ColorBgRaised).
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
					Foreground(ColorPrimary).
					Bold(true)

	// focused+cursor: sidebar focused, cursor on item, not selected.
	SidebarItemFocusedCursorStyle = lipgloss.NewStyle().
					Background(ColorSidebarActive).
					Foreground(ColorTextBright)

	// focused+cursor+selected: sidebar focused, cursor on item, item is selected.
	SidebarItemFocusedCursorSelectedStyle = lipgloss.NewStyle().
						Background(ColorSidebarActive).
						Foreground(ColorTextBright).
						Bold(true)

	// hovered: mouse pointer is over this item (not selected, not cursor).
	SidebarItemHoveredStyle = lipgloss.NewStyle().
				Background(ColorSidebarHover).
				Foreground(ColorTextBright)

	// group header: non-selectable label above a group of tasks.
	SidebarGroupHeaderStyle = lipgloss.NewStyle().
				Background(ColorBgRaised).
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
			Foreground(ColorBg).
			Bold(true).
			Padding(0, 1)

	BtnStopStyle = lipgloss.NewStyle().
			Background(ColorError).
			Foreground(ColorBg).
			Bold(true).
			Padding(0, 1)

	BtnRetryStyle = lipgloss.NewStyle().
			Background(ColorWarning).
			Foreground(ColorBg).
			Bold(true).
			Padding(0, 1)

	BtnRunNowHoverStyle = lipgloss.NewStyle().
				Background(ColorPrimaryHover).
				Foreground(ColorBg).
				Bold(true).
				Padding(0, 1)

	BtnStopHoverStyle = lipgloss.NewStyle().
				Background(ColorErrorHover).
				Foreground(ColorBg).
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
