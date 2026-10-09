// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package home

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// Field identifies a focusable field in the home header.
type Field int

const (
	FieldOpenWebUI Field = iota + 1
	FieldWebUI
	FieldPassword
)

// PasswordMaskWidth matches datadir.GeneratePassword's 22-char base62 output
// so the rendered bullets visually line up with what the operator will see
// in the copy modal.
const PasswordMaskWidth = 22

// RepoURL is where the star button on the Home title row points.
const RepoURL = "https://github.com/runwisp/runwisp"

// StarButtonY is the header row holding the star button (the "Home" title row).
const StarButtonY = 1

const starButtonLabel = "★ Star"

// starButtonSpan returns the panel columns [from, to) of the star button,
// right-aligned with a one-column margin like the Run Now button; ok is false
// when the panel is too narrow to fit it beside the title.
func starButtonSpan(w int) (from, to int, ok bool) {
	to = w - 1
	from = to - lipgloss.Width(uikit.BtnBackStyle.Render(starButtonLabel))
	return from, to, from >= 2+lipgloss.Width("Home")+2
}

// StarButtonAt reports whether panel column x on StarButtonY hits the star
// button of a header rendered at width w.
func StarButtonAt(x, w int) bool {
	from, to, ok := starButtonSpan(w)
	return ok && x >= from && x < to
}

// Fields returns the list of active fields based on the startup info.
// hasLaunchTicket indicates whether the one-click browser open action is available.
func Fields(info uikit.StartupInfo, hasLaunchTicket bool) []Field {
	var fields []Field
	if info.Port > 0 {
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
func RenderHeader(info uikit.StartupInfo, hasLaunchTicket bool, w, homeCursor, homeHover int, starHovered bool) (string, int) {
	var b strings.Builder
	fields := Fields(info, hasLaunchTicket)
	lineCount := 0

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")
	lineCount++

	title := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextBright).
		Bold(true).
		Render("  Home")
	muted := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextMuted)
	// A quiet star button on the title row: mouse-only, no prompt, no network
	// call, and dropped when the panel is too narrow to fit it beside the title.
	if from, _, ok := starButtonSpan(w); ok {
		style := uikit.BtnBackStyle
		if starHovered {
			style = uikit.BtnBackHoverStyle
		}
		title += muted.Render(strings.Repeat(" ", from-lipgloss.Width(title))) + style.Render(starButtonLabel)
	}
	b.WriteString(uikit.PadLine(title, w, uikit.ColorBgLight))
	b.WriteString("\n")
	lineCount++

	var parts []string
	if info.StationEnabled {
		parts = append(parts, muted.Render("Station connected"))
	}
	if info.ConfigStale {
		// Warning color, not muted \u2014 a pending config change is actionable.
		warn := uikit.OnBg(uikit.ColorBgLight, uikit.ColorWarning).
			Render("\u26a0 runwisp.toml changed \u2014 press R to reload")
		parts = append(parts, warn)
	}
	if n := HeldTaskCount(info.Tasks); n > 0 {
		// Warning color: these are the operator's own jobs, loaded and listed, that
		// RunWisp is deliberately not firing. The chip names the way out.
		warn := uikit.OnBg(uikit.ColorBgLight, uikit.ColorWarning).
			Render(fmt.Sprintf("⏸ %d held by cron — `sudo runwisp takeover`", n))
		parts = append(parts, warn)
	}
	if n := len(info.PausedTasks); n > 0 {
		// Warning color like held: a forgotten pause silently stops a job.
		warn := uikit.OnBg(uikit.ColorBgLight, uikit.ColorWarning).
			Render("⏸ " + textutil.Count(n, "schedule", "schedules") + " paused")
		parts = append(parts, warn)
	}
	if n := len(info.ConfigWarnings); n > 0 {
		// Warning color for the same reason as the stale notice: these name jobs the
		// daemon is not running, and nothing else in the TUI would ever mention them.
		warn := uikit.OnBg(uikit.ColorBgLight, uikit.ColorWarning).
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

	writeFieldRows(&b, fields, info, w, homeCursor, homeHover)

	// Auth disabled: render a static, non-focusable Password row so the
	// operator sees the security state at a glance instead of a masked value.
	if info.AuthDisabled {
		renderFieldRow(&b, "Password", "disabled (RUNWISP_AUTH=off)", uikit.ColorWarning, w, false, false)
	}

	if len(fields) > 0 || info.AuthDisabled {
		b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
		b.WriteString("\n")
	}

	return b.String(), fieldsStartY
}

// writeFieldRows renders each focusable field row, applying the cursor/hover
// highlight for the currently selected and hovered indices.
func writeFieldRows(b *strings.Builder, fields []Field, info uikit.StartupInfo, w, homeCursor, homeHover int) {
	for i, f := range fields {
		selected := i == homeCursor
		hovered := i == homeHover && !selected
		switch f {
		case FieldOpenWebUI:
			renderActionRow(b, "Open Web UI", uikit.ColorSecondary, w, selected, hovered)
		case FieldWebUI:
			renderFieldRow(b, "Web UI", info.WebURL(), uikit.ColorText, w, selected, hovered)
		case FieldPassword:
			masked := strings.Repeat("•", PasswordMaskWidth)
			if selected {
				masked += "  (press Enter to copy)"
			}
			renderFieldRow(b, "Password", masked, uikit.ColorWarning, w, selected, hovered)
		}
	}
}

// rowStyle returns the background and left indicator shared by every
// selectable home header row, derived from its selected/hovered state.
func rowStyle(selected, hovered bool) (bg color.Color, indicator string) {
	bg = uikit.ColorBg
	if selected {
		bg = uikit.ColorBgLight
	} else if hovered {
		bg = uikit.ColorExecRowHover
	}

	indicator = "  "
	if selected {
		indicator = "▸ "
	}
	return bg, indicator
}

// renderFieldRow renders a single focusable field row with optional selection highlight.
func renderFieldRow(b *strings.Builder, label, value string, valueColor color.Color, w int, selected, hovered bool) {
	bg, indicator := rowStyle(selected, hovered)

	l := uikit.OnBg(bg, uikit.ColorTextMuted).
		Render(indicator + label)

	sep := lipgloss.NewStyle().
		Background(bg).
		Render("  ")

	v := uikit.OnBg(bg, valueColor).
		Bold(selected).
		Render(value)

	b.WriteString(uikit.PadLine(l+sep+v, w, bg))
	b.WriteString("\n")
}

// renderActionRow renders a primary action row (button-like) with no value — just a label.
func renderActionRow(b *strings.Builder, label string, labelColor color.Color, w int, selected, hovered bool) {
	bg, indicator := rowStyle(selected, hovered)

	l := uikit.OnBg(bg, labelColor).
		Bold(true).
		Render(indicator + "⮕  " + label)

	b.WriteString(uikit.PadLine(l, w, bg))
	b.WriteString("\n")
}

// TaskButton identifies a clickable button on the task header's title row.
type TaskButton int

const (
	TaskButtonNone  TaskButton = iota
	TaskButtonRun              // Run Now (task), Restart or Start (service): key r
	TaskButtonStop             // Stop (running service): key s
	TaskButtonPause            // Pause or Resume (cron schedule): key p
)

// TaskHeader is what the task header shows for one task. Its buttons are the
// task-level actions, and the help bar lists the same set (Hints), so the two
// never disagree about what r, s and p do.
type TaskHeader struct {
	Name    string
	Task    *model.Task
	Paused  bool                 // an operator paused the cron schedule
	Stopped bool                 // an operator stopped the service
	Usage   *model.ResourceUsage // live CPU and memory of running processes
	Loc     *time.Location
	Hovered TaskButton
}

type headerButton struct {
	id    TaskButton
	label string
	key   keys.Binding
	style lipgloss.Style
	hover lipgloss.Style
}

// buttons lists the title-row buttons left to right. Stop needs a running
// service that manual_trigger leaves under operator control; Pause needs a
// pausable schedule that cron doesn't hold (a paused one can always resume).
func (h TaskHeader) buttons() []headerButton {
	t := h.Task
	if t != nil && t.Kind.IsService() {
		if h.Stopped {
			return []headerButton{{TaskButtonRun, "▶ Start (r)", keys.Start, uikit.BtnRunNowStyle, uikit.BtnRunNowHoverStyle}}
		}
		btns := []headerButton{{TaskButtonRun, "↻ Restart (r)", keys.Restart, uikit.BtnRunNowStyle, uikit.BtnRunNowHoverStyle}}
		if t.ManuallyControllable() {
			btns = append(btns, headerButton{TaskButtonStop, "■ Stop (s)", keys.Stop, uikit.BtnStopStyle, uikit.BtnStopHoverStyle})
		}
		return btns
	}
	btns := []headerButton{{TaskButtonRun, "▶ Run Now (r)", keys.RunNow, uikit.BtnRunNowStyle, uikit.BtnRunNowHoverStyle}}
	switch {
	case t == nil || !t.Pausable():
	case h.Paused:
		btns = append(btns, headerButton{TaskButtonPause, "▶ Resume (p)", keys.Resume, uikit.BtnRetryStyle, uikit.BtnRetryHoverStyle})
	case !t.Held():
		btns = append(btns, headerButton{TaskButtonPause, "⏸ Pause (p)", keys.Pause, uikit.BtnBackStyle, uikit.BtnBackHoverStyle})
	}
	return btns
}

// Offers reports whether the header shows button b, i.e. whether its key acts.
func (h TaskHeader) Offers(b TaskButton) bool {
	for _, btn := range h.buttons() {
		if btn.id == b {
			return true
		}
	}
	return false
}

// Hints is the help-bar hints for the header's buttons, e.g. ["r restart", "s stop"].
func (h TaskHeader) Hints() []string {
	btns := h.buttons()
	hints := make([]string, len(btns))
	for i, btn := range btns {
		hints[i] = btn.key.Bar
	}
	return hints
}

// renderButtons renders the buttons one column apart and returns them with
// each button's panel-column span, right-aligned to w with a one-column margin.
func (h TaskHeader) renderButtons(w int) (string, []buttonSpan) {
	btns := h.buttons()
	gap := lipgloss.NewStyle().Background(uikit.ColorBgLight).Render(" ")
	parts := make([]string, 0, 2*len(btns))
	widths := make([]int, len(btns))
	total := len(btns) - 1
	for i, btn := range btns {
		style := btn.style
		if h.Hovered == btn.id {
			style = btn.hover
		}
		rendered := style.Render(btn.label)
		widths[i] = lipgloss.Width(rendered)
		total += widths[i]
		if i > 0 {
			parts = append(parts, gap)
		}
		parts = append(parts, rendered)
	}
	spans := make([]buttonSpan, len(btns))
	x := w - 1 - total
	for i, btn := range btns {
		spans[i] = buttonSpan{btn.id, x, x + widths[i]}
		x += widths[i] + 1
	}
	return strings.Join(parts, ""), spans
}

type buttonSpan struct {
	id       TaskButton
	from, to int
}

// ButtonAt returns the button at panel column x on the title row of a header
// rendered at width w, or TaskButtonNone.
func (h TaskHeader) ButtonAt(x, w int) TaskButton {
	_, spans := h.renderButtons(w)
	for _, s := range spans {
		if x >= s.from && x < s.to {
			return s.id
		}
	}
	return TaskButtonNone
}

// Render draws the task header: the name with the task-level buttons
// right-aligned on the title row, then the schedule line. btnY is the
// header-relative row holding the buttons.
func (h TaskHeader) Render(w int) (out string, btnY int) {
	var b strings.Builder
	task := h.Task

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")

	name := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextBright).
		Bold(true).
		Render("  " + h.Name)
	btns, _ := h.renderButtons(w)
	gap := max(w-lipgloss.Width(name)-lipgloss.Width(btns)-1, 2)
	titleLine := name + uikit.FillBg(gap, uikit.ColorBgLight) + btns
	b.WriteString(uikit.PadLine(titleLine, w, uikit.ColorBgLight))
	b.WriteString("\n")

	schedule := "manual"
	if task != nil {
		schedule = uikit.ScheduleLabel(task)
	}
	held := task != nil && task.HeldBy != model.HeldByNothing
	schedInfo := "  Schedule: " + schedule
	if !held && !h.Paused && task != nil && !task.Kind.IsService() {
		if nextRun := NextCronRun(schedule, h.Loc); nextRun != "" {
			schedInfo += "  •  Next: " + nextRun
		}
	}
	if h.Stopped {
		schedInfo += "  •  stopped"
	}
	if h.Usage != nil {
		schedInfo += "  •  " + uikit.FormatUsage(*h.Usage)
	}
	schedText := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextMuted).
		Render(schedInfo)
	if held {
		// A next-run time would be a lie here: the scheduler stood down for cron, so
		// that tick comes and goes without RunWisp firing anything. This is the only
		// per-task metadata slot in the TUI, so it is where the fact belongs.
		schedText += uikit.OnBg(uikit.ColorBgLight, uikit.ColorWarning).
			Render("  •  ⏸ held — cron still owns this job")
	} else if h.Paused {
		// Same slot and tone as held: the schedule is listed but nothing fires.
		schedText += uikit.OnBg(uikit.ColorBgLight, uikit.ColorWarning).
			Render("  •  ⏸ paused")
	}
	b.WriteString(uikit.PadLine(schedText, w, uikit.ColorBgLight))
	b.WriteString("\n")

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")

	return b.String(), 1
}

// NextCronRun parses a cron schedule expression and returns the next run time
// formatted as "HH:MM:SS (in Xm)" or empty if the schedule is invalid/empty.
// The schedule is evaluated, and the time shown, in loc (the zone the daemon
// runs the task in); nil means the process zone.
func NextCronRun(schedule string, loc *time.Location) string {
	if schedule == "" {
		return ""
	}
	sched, err := cronspec.NewParser().Parse(schedule)
	if err != nil {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	next := sched.Next(time.Now().In(loc))
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

	return next.In(loc).Format("15:04:05") + " (in " + relative + ")"
}

// HeldTaskCount counts the tasks a live cron daemon still owns, which the
// scheduler stood down for. Exported because the header is not the only view that
// needs the count, and deriving it from the task list rather than a separate field
// means it can never disagree with the per-task badge.
func HeldTaskCount(tasks []model.Task) int {
	n := 0
	for _, t := range tasks {
		if t.HeldBy != model.HeldByNothing {
			n++
		}
	}
	return n
}
