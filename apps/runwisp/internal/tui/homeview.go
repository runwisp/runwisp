// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/internal/model"
)

// homeField identifies a focusable field in the home header.
type homeField int

const (
	homeFieldNone homeField = iota
	homeFieldOpenWebUI
	homeFieldWebUI
	homeFieldPassword
)

// passwordMaskWidth matches datadir.GeneratePassword's 22-char base62 output
// so the rendered bullets visually line up with what the operator will see
// in the copy modal.
const passwordMaskWidth = 22

// homeFields returns the list of active fields based on the startup info.
// hasLaunchTicket indicates whether the one-click browser open action is available.
func homeFields(info StartupInfo, hasLaunchTicket bool) []homeField {
	var fields []homeField
	if !info.WebUIDisabled && info.Port > 0 {
		if hasLaunchTicket {
			fields = append(fields, homeFieldOpenWebUI)
		}
		fields = append(fields, homeFieldWebUI)
	}
	if info.PasswordEphemeral && info.Password != "" {
		fields = append(fields, homeFieldPassword)
	}
	return fields
}

// renderHomeHeader renders the server info section for the Home page.
// homeCursor is the index into the focusable fields; -1 means none focused.
// homeHover is the index of the hovered field; -1 means none.
// Returns the rendered string and the 0-based Y line offset where interactive
// field rows begin (used for mouse hit-testing).
func renderHomeHeader(info StartupInfo, hasLaunchTicket bool, w int, homeCursor int, homeHover int) (string, int) {
	var b strings.Builder
	fields := homeFields(info, hasLaunchTicket)
	lineCount := 0

	// Header top padding.
	b.WriteString(padLine("", w, colorBgLight))
	b.WriteString("\n")
	lineCount++

	title := lipgloss.NewStyle().
		Background(colorBgLight).
		Foreground(colorTextBright).
		Bold(true).
		Render("  Home")
	b.WriteString(padLine(title, w, colorBgLight))
	b.WriteString("\n")
	lineCount++

	// Status subtitle.
	var parts []string
	if info.WebUIDisabled {
		parts = append(parts, "Web UI disabled")
	}
	if info.CloudEnabled {
		parts = append(parts, "Cloud connected")
	}
	if len(parts) > 0 {
		sub := lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorTextMuted).
			Render("  " + strings.Join(parts, "  \u00b7  "))
		b.WriteString(padLine(sub, w, colorBgLight))
		b.WriteString("\n")
		lineCount++
	}

	b.WriteString(padLine("", w, colorBgLight))
	b.WriteString("\n")
	lineCount++

	// Spacer between header and fields.
	b.WriteString(padLine("", w, colorBg))
	b.WriteString("\n")
	lineCount++

	fieldsStartY := lineCount

	// Interactive fields.
	for i, f := range fields {
		selected := i == homeCursor
		hovered := i == homeHover && !selected
		switch f {
		case homeFieldOpenWebUI:
			renderHomeActionRow(&b, "Open Web UI", colorSecondary, w, selected, hovered)
		case homeFieldWebUI:
			renderHomeFieldRow(&b, "Web UI", fmt.Sprintf("http://localhost:%d", info.Port), colorText, w, selected, hovered)
		case homeFieldPassword:
			masked := strings.Repeat("•", passwordMaskWidth)
			if selected {
				masked += "  (press Enter to copy)"
			}
			renderHomeFieldRow(&b, "Password", masked, colorWarning, w, selected, hovered)
		}
	}

	// Trailing spacer after fields.
	if len(fields) > 0 {
		b.WriteString(padLine("", w, colorBg))
		b.WriteString("\n")
	}

	return b.String(), fieldsStartY
}

// renderHomeFieldRow renders a single focusable field row with optional selection highlight.
func renderHomeFieldRow(b *strings.Builder, label, value string, valueColor lipgloss.Color, w int, selected bool, hovered bool) {
	bg := colorBg
	if selected {
		bg = colorBgLight
	} else if hovered {
		bg = colorExecRowHover
	}

	indicator := "  "
	if selected {
		indicator = "▸ "
	}

	l := lipgloss.NewStyle().
		Background(bg).
		Foreground(colorTextMuted).
		Render(indicator + label)

	sep := lipgloss.NewStyle().
		Background(bg).
		Render("  ")

	v := lipgloss.NewStyle().
		Background(bg).
		Foreground(valueColor).
		Bold(selected).
		Render(value)

	b.WriteString(padLine(l+sep+v, w, bg))
	b.WriteString("\n")
}

// renderHomeActionRow renders a primary action row (button-like) with no value — just a label.
func renderHomeActionRow(b *strings.Builder, label string, labelColor lipgloss.Color, w int, selected bool, hovered bool) {
	bg := colorBg
	if selected {
		bg = colorBgLight
	} else if hovered {
		bg = colorExecRowHover
	}

	indicator := "  "
	if selected {
		indicator = "▸ "
	}

	l := lipgloss.NewStyle().
		Background(bg).
		Foreground(labelColor).
		Bold(true).
		Render(indicator + "⮕  " + label)

	b.WriteString(padLine(l, w, bg))
	b.WriteString("\n")
}

// renderTaskHeader renders the task info header with a Run Now button.
// The runNowBtnY output is the screen-relative Y offset of the Run Now button row
// within this header (0-based from header start).
func renderTaskHeader(taskName string, task *model.TaskBrief, w int, runNowHovered bool) (string, int) {
	var b strings.Builder
	lineCount := 0

	// Header top padding.
	b.WriteString(padLine("", w, colorBgLight))
	b.WriteString("\n")
	lineCount++

	// Task name.
	name := lipgloss.NewStyle().
		Background(colorBgLight).
		Foreground(colorTextBright).
		Bold(true).
		Render("  " + taskName)
	b.WriteString(padLine(name, w, colorBgLight))
	b.WriteString("\n")
	lineCount++

	// Schedule + Run Now button.
	schedule := "manual"
	switch {
	case task != nil && task.Kind.IsService():
		schedule = fmt.Sprintf("service x%d", task.Instances)
	case task != nil && task.Cron != "":
		schedule = task.Cron
	}
	schedInfo := "  Schedule: " + schedule
	if task != nil && !task.Kind.IsService() {
		if nextRun := nextCronRun(schedule); nextRun != "" {
			schedInfo += "  •  Next: " + nextRun
		}
	}
	schedText := lipgloss.NewStyle().
		Background(colorBgLight).
		Foreground(colorTextMuted).
		Render(schedInfo)

	style := btnRunNowStyle
	if runNowHovered {
		style = btnRunNowHoverStyle
	}
	btnLabel := "▶ Run Now (r)"
	if task != nil && task.Kind.IsService() {
		btnLabel = "↻ Restart (r)"
	}
	btn := style.Render(btnLabel)

	schedWidth := lipgloss.Width(schedText)
	btnWidth := lipgloss.Width(btn)
	gap := w - schedWidth - btnWidth - 1
	if gap < 2 {
		gap = 2
	}

	schedLine := schedText +
		lipgloss.NewStyle().Background(colorBgLight).Render(strings.Repeat(" ", gap)) +
		btn
	btnLineY := lineCount
	b.WriteString(padLine(schedLine, w, colorBgLight))
	b.WriteString("\n")
	lineCount++

	// Bottom padding.
	b.WriteString(padLine("", w, colorBgLight))
	b.WriteString("\n")

	return b.String(), btnLineY
}

// nextCronRun parses a cron schedule expression and returns the next run time
// formatted as "HH:MM:SS (in Xm)" or empty if the schedule is invalid/empty.
func nextCronRun(schedule string) string {
	if schedule == "" {
		return ""
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := parser.Parse(schedule)
	if err != nil {
		return ""
	}
	next := sched.Next(time.Now())
	dur := time.Until(next)

	var relative string
	switch {
	case dur < time.Minute:
		relative = fmt.Sprintf("%ds", int(dur.Seconds()))
	case dur < time.Hour:
		relative = fmt.Sprintf("%dm", int(dur.Minutes()))
	case dur < 24*time.Hour:
		h := int(dur.Hours())
		m := int(dur.Minutes()) % 60
		if m > 0 {
			relative = fmt.Sprintf("%dh %dm", h, m)
		} else {
			relative = fmt.Sprintf("%dh", h)
		}
	default:
		d := int(dur.Hours()) / 24
		h := int(dur.Hours()) % 24
		if h > 0 {
			relative = fmt.Sprintf("%dd %dh", d, h)
		} else {
			relative = fmt.Sprintf("%dd", d)
		}
	}

	return next.Format("15:04:05") + " (in " + relative + ")"
}
