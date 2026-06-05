// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package home

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// Field identifies a focusable field in the home header.
type Field int

const (
	FieldNone Field = iota
	FieldOpenWebUI
	FieldWebUI
	FieldPassword
)

// PasswordMaskWidth matches datadir.GeneratePassword's 22-char base62 output
// so the rendered bullets visually line up with what the operator will see
// in the copy modal.
const PasswordMaskWidth = 22

// Fields returns the list of active fields based on the startup info.
// hasLaunchTicket indicates whether the one-click browser open action is available.
func Fields(info uikit.StartupInfo, hasLaunchTicket bool) []Field {
	var fields []Field
	if !info.WebUIDisabled && info.Port > 0 {
		if hasLaunchTicket {
			fields = append(fields, FieldOpenWebUI)
		}
		fields = append(fields, FieldWebUI)
	}
	// No password row to copy when auth is disabled — the daemon still mints
	// one internally, but it gates nothing and must not be presented as a
	// credential.
	if !info.AuthDisabled && info.PasswordEphemeral && info.Password != "" {
		fields = append(fields, FieldPassword)
	}
	return fields
}

// RenderHeader renders the server info section for the Home page.
// homeCursor is the index into the focusable fields; -1 means none focused.
// homeHover is the index of the hovered field; -1 means none.
// Returns the rendered string and the 0-based Y line offset where interactive
// field rows begin (used for mouse hit-testing).
func RenderHeader(info uikit.StartupInfo, hasLaunchTicket bool, w int, homeCursor int, homeHover int) (string, int) {
	var b strings.Builder
	fields := Fields(info, hasLaunchTicket)
	lineCount := 0

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")
	lineCount++

	title := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextBright).
		Bold(true).
		Render("  Home")
	b.WriteString(uikit.PadLine(title, w, uikit.ColorBgLight))
	b.WriteString("\n")
	lineCount++

	muted := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted)
	var parts []string
	if info.WebUIDisabled {
		parts = append(parts, muted.Render("Web UI disabled"))
	}
	if info.CloudEnabled {
		parts = append(parts, muted.Render("Cloud connected"))
	}
	if info.ConfigStale {
		// Warning color, not muted \u2014 a pending config change is actionable.
		warn := lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorWarning).
			Render("\u26a0 runwisp.toml changed \u2014 run 'runwisp restart' to apply")
		parts = append(parts, warn)
	}
	if len(parts) > 0 {
		sub := muted.Render("  ") + strings.Join(parts, muted.Render("  \u00b7  "))
		b.WriteString(uikit.PadLine(sub, w, uikit.ColorBgLight))
		b.WriteString("\n")
		lineCount++
	}

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")
	lineCount++

	b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
	b.WriteString("\n")
	lineCount++

	fieldsStartY := lineCount

	for i, f := range fields {
		selected := i == homeCursor
		hovered := i == homeHover && !selected
		switch f {
		case FieldOpenWebUI:
			renderActionRow(&b, "Open Web UI", uikit.ColorSecondary, w, selected, hovered)
		case FieldWebUI:
			renderFieldRow(&b, "Web UI", fmt.Sprintf("http://localhost:%d", info.Port), uikit.ColorText, w, selected, hovered)
		case FieldPassword:
			masked := strings.Repeat("•", PasswordMaskWidth)
			if selected {
				masked += "  (press Enter to copy)"
			}
			renderFieldRow(&b, "Password", masked, uikit.ColorWarning, w, selected, hovered)
		}
	}

	// Auth disabled: render a static, non-focusable Password row so the
	// operator sees the security state at a glance instead of a masked value.
	if info.AuthDisabled {
		renderFieldRow(&b, "Password", "disabled (RUNWISP_NO_AUTH)", uikit.ColorWarning, w, false, false)
	}

	if len(fields) > 0 || info.AuthDisabled {
		b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
		b.WriteString("\n")
	}

	return b.String(), fieldsStartY
}

// renderFieldRow renders a single focusable field row with optional selection highlight.
func renderFieldRow(b *strings.Builder, label, value string, valueColor lipgloss.Color, w int, selected bool, hovered bool) {
	bg := uikit.ColorBg
	if selected {
		bg = uikit.ColorBgLight
	} else if hovered {
		bg = uikit.ColorExecRowHover
	}

	indicator := "  "
	if selected {
		indicator = "▸ "
	}

	l := lipgloss.NewStyle().
		Background(bg).
		Foreground(uikit.ColorTextMuted).
		Render(indicator + label)

	sep := lipgloss.NewStyle().
		Background(bg).
		Render("  ")

	v := lipgloss.NewStyle().
		Background(bg).
		Foreground(valueColor).
		Bold(selected).
		Render(value)

	b.WriteString(uikit.PadLine(l+sep+v, w, bg))
	b.WriteString("\n")
}

// renderActionRow renders a primary action row (button-like) with no value — just a label.
func renderActionRow(b *strings.Builder, label string, labelColor lipgloss.Color, w int, selected bool, hovered bool) {
	bg := uikit.ColorBg
	if selected {
		bg = uikit.ColorBgLight
	} else if hovered {
		bg = uikit.ColorExecRowHover
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

	b.WriteString(uikit.PadLine(l, w, bg))
	b.WriteString("\n")
}

// RenderTaskHeader renders the task info header with a Run Now button.
// The runNowBtnY output is the screen-relative Y offset of the Run Now button row
// within this header (0-based from header start).
func RenderTaskHeader(taskName string, task *model.TaskBrief, w int, runNowHovered bool) (string, int) {
	var b strings.Builder
	lineCount := 0

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")
	lineCount++

	name := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextBright).
		Bold(true).
		Render("  " + taskName)
	b.WriteString(uikit.PadLine(name, w, uikit.ColorBgLight))
	b.WriteString("\n")
	lineCount++

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
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted).
		Render(schedInfo)

	style := uikit.BtnRunNowStyle
	if runNowHovered {
		style = uikit.BtnRunNowHoverStyle
	}
	btnLabel := "▶ Run Now (r)"
	if task != nil && task.Kind.IsService() {
		btnLabel = "↻ Restart (r)"
	}
	btn := style.Render(btnLabel)

	schedWidth := lipgloss.Width(schedText)
	btnWidth := lipgloss.Width(btn)
	gap := max(w-schedWidth-btnWidth-1, 2)

	schedLine := schedText +
		lipgloss.NewStyle().Background(uikit.ColorBgLight).Render(strings.Repeat(" ", gap)) +
		btn
	btnLineY := lineCount
	b.WriteString(uikit.PadLine(schedLine, w, uikit.ColorBgLight))
	b.WriteString("\n")
	lineCount++

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")

	return b.String(), btnLineY
}

// nextCronRun parses a cron schedule expression and returns the next run time
// formatted as "HH:MM:SS (in Xm)" or empty if the schedule is invalid/empty.
func nextCronRun(schedule string) string {
	if schedule == "" {
		return ""
	}
	sched, err := cronspec.NewParser().Parse(schedule)
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
