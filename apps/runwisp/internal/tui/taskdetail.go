// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/home"
)

// taskHealth holds the per-task run figures shown in the detail panel. It starts
// empty (loaded=false → "loading…") and is filled in by an async TaskSummaryMsg.
type taskHealth struct {
	loaded  bool
	summary uikit.TaskSummaryMsg
}

// TaskDetailDialog is the on-demand task inspector — a centered modal that shows
// a task's definition (schedule, concurrency, dependencies, parameters) and a
// recent-health line, opened with `i` when a task is in focus. It exists so the
// always-visible task header can stay thin: the detail lives behind a keypress
// instead of polluting the header.
type TaskDetailDialog struct {
	taskName string
	task     *model.TaskBrief
	health   taskHealth
}

// NewTaskDetailDialog builds the inspector for a task. task may be nil when the
// definition isn't in the local cache; the panel degrades to name + health.
func NewTaskDetailDialog(taskName string, task *model.TaskBrief) TaskDetailDialog {
	return TaskDetailDialog{taskName: taskName, task: task}
}

// ApplySummary stores health figures delivered for this task. A message for a
// different task (a stale fetch from a previously-open panel) is ignored.
func (d *TaskDetailDialog) ApplySummary(msg uikit.TaskSummaryMsg) {
	if msg.TaskName != d.taskName {
		return
	}
	d.health = taskHealth{loaded: true, summary: msg}
}

// Update reports true when the dialog should close.
func (d *TaskDetailDialog) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "i", "esc", "enter", "q":
			return true
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			return true
		}
	}
	return false
}

func (d *TaskDetailDialog) View(screenWidth, screenHeight int) string {
	const labelCol = 14
	dialogWidth, innerWidth := modalDimensions(screenWidth, 58, 44)

	row := func(label, value string, color lipgloss.Color) string {
		return taskDetailRow(label, value, color, labelCol, innerWidth)
	}

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine(d.taskName, innerWidth, uikit.ColorTextBright, true),
		modalEmptyLine(innerWidth),
	}
	lines = append(lines, d.definitionRows(row, innerWidth)...)
	lines = append(lines,
		modalEmptyLine(innerWidth),
		taskDetailSectionLine("Recent health", innerWidth),
	)
	lines = append(lines, d.healthRows(row)...)
	lines = append(lines,
		modalEmptyLine(innerWidth),
		modalSurfaceLine("i / esc close", innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorPrimary, lines)
	return box.view
}

// definitionRows renders the static task definition: kind, schedule, concurrency
// and any dependencies/parameters. Each field is shown only when it carries
// information, so a plain cron task stays compact.
func (d *TaskDetailDialog) definitionRows(row func(label, value string, color lipgloss.Color) string, innerWidth int) []string {
	task := d.task
	if task == nil {
		return []string{row("", "definition unavailable", uikit.ColorTextMuted)}
	}

	var out []string
	add := func(label, value string) {
		if value != "" {
			out = append(out, row(label, value, uikit.ColorText))
		}
	}

	d.kindRows(add)
	add("Group", task.Group)
	if task.APITrigger {
		add("API trigger", "enabled")
	}
	if len(task.DependsOn) > 0 {
		add("Depends on", strings.Join(task.DependsOn, ", "))
	}
	if task.Compose != nil {
		ref := task.Compose.File
		if task.Compose.Service != "" {
			ref += " (" + task.Compose.Service + ")"
		}
		add("Compose", ref)
	}
	if len(task.Parameters) > 0 {
		add("Parameters", strings.Join(paramNames(task.Parameters), ", "))
	}
	return out
}

// kindRows feeds the kind-specific definition fields into add: a service shows
// its instance count and restart policy; a scheduled task shows its schedule,
// next fire, concurrency and catch-up. Split out of definitionRows so neither
// branch's optional fields nest under the other.
func (d *TaskDetailDialog) kindRows(add func(label, value string)) {
	task := d.task
	if task.Kind.IsService() {
		add("Kind", "service")
		add("Instances", strconv.Itoa(max(task.Instances, 1)))
		add("Restart", string(task.Restart))
		return
	}
	add("Kind", "task")
	schedule := "manual"
	if task.Cron != "" {
		schedule = task.Cron
	}
	add("Schedule", schedule)
	add("Next run", home.NextCronRun(task.Cron))
	if task.MaxConcurrent > 0 {
		add("Concurrency", fmt.Sprintf("max %d · %s", task.MaxConcurrent, overlapLabel(task.OnOverlap)))
	}
	add("Catch-up", string(task.CatchUp))
}

// healthRows renders the recent-run breakdown delivered by FetchTaskSummary.
func (d *TaskDetailDialog) healthRows(row func(label, value string, color lipgloss.Color) string) []string {
	if !d.health.loaded {
		return []string{row("", "loading…", uikit.ColorTextMuted)}
	}
	s := d.health.summary
	if s.Err != nil {
		return []string{row("", "unavailable", uikit.ColorTextMuted)}
	}

	out := []string{row("Total runs", strconv.FormatInt(s.Total, 10), uikit.ColorText)}
	if s.Window == 0 {
		return append(out, row("", "no runs yet", uikit.ColorTextMuted))
	}

	breakdown := seg(fmt.Sprintf("%d ok", s.Success), uikit.ColorSuccess) +
		seg(" · ", uikit.ColorTextMuted) +
		seg(fmt.Sprintf("%d failed", s.Failed), failureColor(s.Failed))
	if s.Other > 0 {
		breakdown += seg(fmt.Sprintf(" · %d other", s.Other), uikit.ColorTextMuted)
	}
	out = append(out, row(fmt.Sprintf("Last %d", s.Window), breakdown, uikit.ColorText))

	if rate, ok := successRate(s.Success, s.Failed); ok {
		out = append(out, row("Success rate", rate, uikit.ColorText))
	}
	if s.LastFailure != nil {
		out = append(out, row("Last failure", uikit.FormatTimeAgo(*s.LastFailure), uikit.ColorWarning))
	} else {
		out = append(out, row("Last failure", "none", uikit.ColorSuccess))
	}
	return out
}

// overlapLabel renders the concurrency policy, falling back to its zero value's
// effective default so the panel never shows an empty policy.
func overlapLabel(p model.ConcurrencyPolicy) string {
	if p == "" {
		return "queue"
	}
	return string(p)
}

// failureColor dims the failure count when there are no failures so a healthy
// task doesn't paint a red zero.
func failureColor(failed int) lipgloss.Color {
	if failed == 0 {
		return uikit.ColorTextMuted
	}
	return uikit.ColorError
}

// successRate reports the success percentage over decided (non-pending/running)
// runs, and false when nothing has finished yet.
func successRate(success, failed int) (string, bool) {
	decided := success + failed
	if decided == 0 {
		return "", false
	}
	return fmt.Sprintf("%d%%", success*100/decided), true
}

func paramNames(params []model.TaskParam) []string {
	names := make([]string, len(params))
	for i := range params {
		names[i] = params[i].Key
	}
	return names
}

// seg renders a colored inline segment on the modal surface background so
// composed health strings keep the dialog's fill behind each piece.
func seg(text string, color lipgloss.Color) string {
	return lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(color).
		Render(text)
}

// taskDetailSectionLine renders a left-aligned bold section header inside the modal.
func taskDetailSectionLine(title string, innerWidth int) string {
	return lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorSecondary).
		Bold(true).
		Width(innerWidth).
		Render(title)
}

// taskDetailRow renders a "label  value" line with a fixed label column. The
// value is clipped to whatever width remains so a long path or dependency list
// can't wrap and break the modal box.
func taskDetailRow(label, value string, valueColor lipgloss.Color, labelCol, innerWidth int) string {
	labelCell := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted).
		Width(labelCol).
		Render(label)
	value = clipToWidth(value, innerWidth-labelCol)
	valueCell := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(valueColor).
		Render(value)
	return uikit.PadLine(labelCell+valueCell, innerWidth, uikit.ColorBgLight)
}

// clipToWidth truncates s to at most w display columns, appending an ellipsis
// when it had to cut. Pre-styled segments (which carry ANSI) are measured by
// their visible width, so a styled value that already fits is returned as-is.
func clipToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
