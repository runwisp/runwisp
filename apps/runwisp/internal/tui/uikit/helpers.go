// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"fmt"
	"image/color"
	"time"

	"github.com/runwisp/runwisp/internal/config"
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
func PadLine(content string, width int, bg color.Color) string {
	contentWidth := VisibleWidth(content)
	if contentWidth >= width {
		return content
	}
	return content + FillBg(width-contentWidth, bg)
}

// FormatDuration renders a run's elapsed time. When EndedAt is nil the duration
// is measured against time.Now() so live cells tick forward.
func FormatDuration(run model.Run) string {
	if run.StartedAt == nil {
		return "—"
	}
	endTime := time.Now()
	if run.EndedAt != nil {
		endTime = *run.EndedAt
	}
	d := endTime.Sub(*run.StartedAt)
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

// FormatUsage renders live usage as "CPU 12% · 48 MB" (100% is one core).
func FormatUsage(u model.ResourceUsage) string {
	return fmt.Sprintf("CPU %.0f%% · %s", u.CPUPercent, config.FormatByteSize(u.MemoryBytes))
}

// ResolveLocation returns the zone for an IANA name (the daemon's resolved
// timezone), falling back to the process zone when the name is empty or unknown.
func ResolveLocation(name string) *time.Location {
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}

// FormatTimestamp renders a timestamp as "2006-01-02 15:04:05" in loc. A nil loc
// means the process zone.
func FormatTimestamp(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02 15:04:05")
}

// RelativeTime returns a short human string ("just now", "5m ago",
// "yesterday", "3d ago"). Keep parity with
// apps/ui/src/lib/utils/notification-rhythm.ts.
func RelativeTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 30*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		months := int(d.Hours() / 24 / 30)
		if months <= 1 {
			return "1mo ago"
		}
		return fmt.Sprintf("%dmo ago", months)
	default:
		years := int(d.Hours() / 24 / 365)
		if years <= 1 {
			return "1y ago"
		}
		return fmt.Sprintf("%dy ago", years)
	}
}

// FormatTimeAgo renders a short relative-time label ("5m ago", "Jan 02 15:04").
// Past a day it shows the absolute time in loc (nil means the process zone).
func FormatTimeAgo(t time.Time, loc *time.Location) string {
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
		if loc == nil {
			loc = time.Local
		}
		return t.In(loc).Format("Jan 02 15:04")
	}
}
