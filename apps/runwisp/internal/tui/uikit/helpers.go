// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package uikit

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/model"
)

// SidebarWidth is the fixed width of the left sidebar panel in cells.
const SidebarWidth = 28

// MaxContentWidth bounds the main content panel on wide terminals so the table
// and header don't stretch edge-to-edge. The panel is left-aligned against the
// sidebar; any extra width is filled with the app background.
const MaxContentWidth = 110

// PadLine right-pads content with a styled-background space run so the row
// fills `width` cells without losing the background colour when terminals
// trim trailing whitespace.
func PadLine(content string, width int, bg lipgloss.Color) string {
	contentWidth := lipgloss.Width(content)
	if contentWidth >= width {
		return content
	}
	return content + lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", width-contentWidth))
}

// FormatDuration renders a run's elapsed time. When EndAt is nil the duration
// is measured against time.Now() so live cells tick forward.
func FormatDuration(run model.Run) string {
	if run.StartAt == nil {
		return "—"
	}
	endTime := time.Now()
	if run.EndAt != nil {
		endTime = *run.EndAt
	}
	d := endTime.Sub(*run.StartAt)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hrs := int(d.Hours())
	mins := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hrs, mins)
}

// FormatTimeAgo renders a short relative-time label ("5m ago", "Jan 02 15:04").
func FormatTimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("Jan 02 15:04")
	}
}
