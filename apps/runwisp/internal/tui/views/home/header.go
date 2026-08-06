// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package home

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/textutil"
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
func RenderHeader(info uikit.StartupInfo, hasLaunchTicket bool, w, homeCursor, homeHover int) (string, int) {
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
			Render("\u26a0 runwisp.toml changed \u2014 press R to reload")
		parts = append(parts, warn)
	}
	if n := HeldTaskCount(info.Tasks); n > 0 {
		// Warning color: these are the operator's own jobs, loaded and listed, that
		// RunWisp is deliberately not firing. The chip names the way out.
		warn := lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorWarning).
			Render(fmt.Sprintf("⏸ %d held by cron — `sudo runwisp takeover`", n))
		parts = append(parts, warn)
	}
	if n := len(info.ConfigWarnings); n > 0 {
		// Warning color for the same reason as the stale notice: these name jobs the
		// daemon is not running, and nothing else in the TUI would ever mention them.
		warn := lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorWarning).
			Render(fmt.Sprintf("\u26a0 %s \u2014 see `runwisp validate`", textutil.Count(n, "config warning", "config warnings")))
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
			renderFieldRow(&b, "Web UI", info.WebURL(), uikit.ColorText, w, selected, hovered)
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
func renderFieldRow(b *strings.Builder, label, value string, valueColor lipgloss.Color, w int, selected, hovered bool) {
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
func renderActionRow(b *strings.Builder, label string, labelColor lipgloss.Color, w int, selected, hovered bool) {
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
	held := task != nil && task.HeldBy != model.HeldByNothing
	schedInfo := "  Schedule: " + schedule
	if !held && task != nil && !task.Kind.IsService() {
		if nextRun := NextCronRun(schedule); nextRun != "" {
			schedInfo += "  •  Next: " + nextRun
		}
	}
	schedText := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted).
		Render(schedInfo)
	if held {
		// A next-run time would be a lie here: the scheduler stood down for cron, so
		// that tick comes and goes without RunWisp firing anything. This is the only
		// per-task metadata slot in the TUI, so it is where the fact belongs.
		schedText += lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorWarning).
			Render("  •  ⏸ held — cron still owns this job")
	}

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

// NextCronRun parses a cron schedule expression and returns the next run time
// formatted as "HH:MM:SS (in Xm)" or empty if the schedule is invalid/empty.
func NextCronRun(schedule string) string {
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

// HeldTaskCount counts the tasks a live cron daemon still owns, which the
// scheduler stood down for. Exported because the header is not the only view that
// needs the count, and deriving it from the task list rather than a separate field
// means it can never disagree with the per-task badge.
func HeldTaskCount(tasks []model.TaskBrief) int {
	n := 0
	for _, t := range tasks {
		if t.HeldBy != model.HeldByNothing {
			n++
		}
	}
	return n
}
