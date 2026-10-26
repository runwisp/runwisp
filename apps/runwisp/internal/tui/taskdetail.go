// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/home"
)

// taskHealth holds the per-task run figures shown in the detail panel. It starts
// empty (loaded=false → "loading…") and is filled in by an async TaskSummaryMsg.
type taskHealth struct {
	loaded  bool
	summary uikit.TaskSummaryMsg
}

// TaskDetailDialog is the on-demand task inspector — a centered modal that shows
// a task's run command, its definition (schedule, concurrency, dependencies,
// parameters, retention) and a recent-health block, opened with `i` when a task
// is in focus. The command shows its first lines; `c` expands it into a
// full-width view. Its body scrolls when it outgrows the screen. It exists so the
// always-visible task header can stay thin: the detail lives behind a keypress
// instead of polluting the header.
type TaskDetailDialog struct {
	taskName string
	task     *model.Task
	// command is the task's run command, empty for container and compose
	// tasks or before the first /api/tasks poll lands.
	command string
	health  taskHealth
	scroll  modalScroll
	// commandOpen swaps the inspector for the full-width command view, which
	// keeps its own scroll so closing it returns to the same spot.
	commandOpen   bool
	commandScroll modalScroll
	// paused reports that an operator paused the task's cron schedule.
	paused bool
	// loc is the zone the task's schedule is evaluated and shown in.
	loc *time.Location
}

// NewTaskDetailDialog builds the inspector for a task. task may be nil when the
// definition isn't in the local cache; the panel degrades to name + health.
func NewTaskDetailDialog(taskName string, task *model.Task) *TaskDetailDialog {
	return &TaskDetailDialog{taskName: taskName, task: task}
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
func (d *TaskDetailDialog) Update(msg tea.Msg) (tea.Cmd, bool) {
	scroll := &d.scroll
	if d.commandOpen {
		scroll = &d.commandScroll
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch key := msg.String(); {
		case d.commandOpen && (key == "c" || key == "esc" || key == "q"):
			d.commandOpen = false
		case key == "i" || key == "esc" || key == "enter" || key == "q":
			return nil, true
		case key == "c" && d.command != "":
			d.commandOpen = true
		default:
			scroll.handleKey(key)
		}
	case tea.MouseWheelMsg:
		scroll.handleWheel(msg)
	case tea.MouseClickMsg:
		if d.commandOpen {
			d.commandOpen = false
			return nil, false
		}
		return nil, true
	}
	return nil, false
}

func (d *TaskDetailDialog) View(screenWidth, screenHeight int) string {
	if d.commandOpen {
		return d.commandView(screenWidth, screenHeight)
	}
	// Fits the longest label ("Manual trigger", "Manual control") plus a gap.
	const labelCol = 16
	dialogWidth, innerWidth := modalDimensions(screenWidth, 58, 44)

	row := func(label, value string, color color.Color) string {
		return taskDetailRow(label, value, color, labelCol, innerWidth)
	}

	// The name stays put; everything below it scrolls.
	body := d.descriptionLines(innerWidth)
	body = append(body, modalEmptyLine(innerWidth))
	closeHint := "i / esc close"
	if d.command != "" {
		body = append(body, modalSectionLine("Command", innerWidth))
		body = append(body, commandPreviewLines(d.command, innerWidth)...)
		body = append(body, modalEmptyLine(innerWidth))
		closeHint = "c full command · " + closeHint
	}
	body = append(body, d.definitionRows(row)...)
	if retention := d.retentionRows(row); len(retention) > 0 {
		body = append(body, modalEmptyLine(innerWidth), modalSectionLine("Output and retention", innerWidth))
		body = append(body, retention...)
	}
	body = append(body,
		modalEmptyLine(innerWidth),
		modalSectionLine("Recent health", innerWidth),
	)
	body = append(body, d.healthRows(row)...)

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine(d.taskName, innerWidth, uikit.ColorTextBright, true),
	}
	lines = append(lines, d.scroll.window(body, modalBodyRows(screenHeight))...)
	lines = append(lines, modalFooter(d.scroll.hint(closeHint), innerWidth)...)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorPrimary, lines)
	return box.view
}

// commandView is the expanded command: the whole script in a modal as wide as
// the screen allows, so long lines wrap as little as possible.
func (d *TaskDetailDialog) commandView(screenWidth, screenHeight int) string {
	dialogWidth, innerWidth := modalDimensions(screenWidth, screenWidth, 44)
	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine(d.taskName+" · command", innerWidth, uikit.ColorTextBright, true),
	}
	lines = append(lines, d.commandScroll.window(commandLines(d.command, innerWidth), modalBodyRows(screenHeight))...)
	lines = append(lines, modalFooter(d.commandScroll.hint("c / esc back"), innerWidth)...)
	return renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorPrimary, lines).view
}

// maxDescriptionLines caps the wrapped description so a long one doesn't
// push the definition out of view below the name.
const maxDescriptionLines = 3

// descriptionLines renders the task's description under its name, wrapped to the
// modal width and clipped with an ellipsis past maxDescriptionLines.
func (d *TaskDetailDialog) descriptionLines(innerWidth int) []string {
	if d.task == nil || strings.TrimSpace(d.task.Description) == "" {
		return nil
	}
	wrapped := strings.Split(ansi.Wrap(strings.TrimSpace(d.task.Description), innerWidth-4, ""), "\n")
	if len(wrapped) > maxDescriptionLines {
		wrapped = wrapped[:maxDescriptionLines]
		wrapped[maxDescriptionLines-1] = uikit.TruncateToWidth(wrapped[maxDescriptionLines-1]+"…", innerWidth-4)
	}
	out := make([]string, len(wrapped))
	for i, l := range wrapped {
		out[i] = modalSurfaceLine(l, innerWidth, uikit.ColorTextMuted, false)
	}
	return out
}

// commandPreviewLines is the inspector's glimpse of the run command: its first
// commandPreviewRows lines, each clipped to one row, then a muted pointer to
// the full view when anything was cut.
func commandPreviewLines(command string, innerWidth int) []string {
	src := strings.Split(strings.TrimRight(command, "\n"), "\n")
	shown := src[:min(len(src), commandPreviewRows)]
	clipped := len(src) > len(shown)
	out := make([]string, 0, len(shown)+1)
	for _, l := range shown {
		if ansi.StringWidth(l) > innerWidth {
			clipped = true
		}
		out = append(out, modalLeftLine(uikit.TruncateToWidth(l, innerWidth), innerWidth, uikit.ColorText))
	}
	if clipped {
		more := "press c for the full command"
		if hidden := len(src) - len(shown); hidden > 0 {
			more = fmt.Sprintf("… %d more lines · %s", hidden, more)
		}
		out = append(out, modalLeftLine(more, innerWidth, uikit.ColorTextMuted))
	}
	return out
}

// commandPreviewRows is how many lines of the run command the inspector shows
// before pointing at the full view.
const commandPreviewRows = 3

// commandLines renders the whole run command, left-aligned and wrapped to the
// modal width, keeping its own line breaks and indentation.
func commandLines(command string, innerWidth int) []string {
	wrapped := strings.Split(ansi.Wrap(strings.TrimRight(command, "\n"), innerWidth, ""), "\n")
	out := make([]string, len(wrapped))
	for i, l := range wrapped {
		out[i] = modalLeftLine(l, innerWidth, uikit.ColorText)
	}
	return out
}

// definitionRows renders the static task definition: kind, schedule, concurrency
// and any dependencies/parameters. Each field is shown only when it carries
// information, so a plain cron task stays compact.
func (d *TaskDetailDialog) definitionRows(row func(label, value string, color color.Color) string) []string {
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
	if timeout := task.TimeoutValue(); timeout > 0 {
		add("Timeout", textutil.FormatDuration(timeout))
	}
	add("Group", task.Group)
	if task.Triggerable() {
		add("Manual trigger", "enabled")
	}
	if len(task.DependsOn) > 0 {
		add("Depends on", strings.Join(task.DependsOn, ", "))
	}
	if task.Compose != nil {
		ref := task.Compose.File
		switch {
		case ref == "":
			// The sidecar form: no file, the service of RunWisp's own project.
			ref = task.Compose.Service
		case task.Compose.Service != "":
			ref += " (" + task.Compose.Service + ")"
		}
		add("Compose", ref)
	}
	if task.Source.Promotable() {
		// Provenance, not a warning: an imported or cron-sourced task runs like any
		// other. Two rows rather than one because the value column clips around 40
		// columns, and the command is the half an operator actually needs. The TUI
		// only ever names that command — writing TOML is the CLI's job.
		add("Source", taskSourceLabel(task.Source, task.SourceFile))
		add("Promote", "runwisp promote "+task.Name)
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
		if !task.ManuallyControllable() {
			add("Manual control", "locked (edit runwisp.toml + reload)")
		}
		return
	}
	add("Kind", "task")
	add("Schedule", uikit.ScheduleLabel(task))
	if d.paused {
		add("Next run", "paused")
	} else {
		add("Next run", home.NextCronRun(task.Cron, d.loc))
	}
	if task.MaxConcurrent > 0 {
		add("Concurrency", fmt.Sprintf("max %d · %s", task.MaxConcurrent, overlapLabel(task.OnOverlap)))
	}
	add("Catch-up", catchUpLabel(task.CatchUpValue()))
}

// retentionRows renders the output and retention settings: the per-run log cap
// and how many runs, or how long, the task's history is kept. Retention left
// uncapped has no row.
func (d *TaskDetailDialog) retentionRows(row func(label, value string, color color.Color) string) []string {
	task := d.task
	if task == nil {
		return nil
	}
	var out []string
	if task.LogMaxSize > 0 {
		limit := config.FormatByteSize(task.LogMaxSize)
		if task.LogOnFull != "" {
			limit += " · " + task.LogOnFull
		}
		out = append(out, row("Log limit", limit, uikit.ColorText))
	}
	if task.KeepRuns != nil {
		out = append(out, row("Keep runs", strconv.Itoa(*task.KeepRuns), uikit.ColorText))
	}
	if task.KeepFor > 0 {
		out = append(out, row("Keep for", retentionLabel(task.KeepFor), uikit.ColorText))
	}
	return out
}

// retentionLabel renders a keep_for window in whole days when it is one
// ("120d", not "2880h"), the unit retention is usually written in.
func retentionLabel(d time.Duration) string {
	const day = 24 * time.Hour
	if d%day == 0 {
		return fmt.Sprintf("%dd", d/day)
	}
	return textutil.FormatDuration(d)
}

// catchUpLabel renders the integer catch_up value as an operator-facing phrase.
func catchUpLabel(n int) string {
	switch {
	case n <= 0:
		return "skip"
	case n == 1:
		return "most recent"
	default:
		return fmt.Sprintf("up to %d", n)
	}
}

// healthRows renders the recent-run breakdown delivered by FetchTaskSummary.
func (d *TaskDetailDialog) healthRows(row func(label, value string, color color.Color) string) []string {
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

	breakdown := modalSeg(fmt.Sprintf("%d ok", s.Success), uikit.ColorSuccess) +
		modalSeg(" · ", uikit.ColorTextMuted) +
		modalSeg(fmt.Sprintf("%d failed", s.Failed), failureColor(s.Failed))
	if s.Other > 0 {
		breakdown += modalSeg(fmt.Sprintf(" · %d other", s.Other), uikit.ColorTextMuted)
	}
	out = append(out, row(fmt.Sprintf("Last %d", s.Window), breakdown, uikit.ColorText))

	if rate, ok := successRate(s.Success, s.Failed); ok {
		out = append(out, row("Success rate", rate, uikit.ColorText))
	}
	if s.LastFailure != nil {
		out = append(out, row("Last failure", uikit.FormatTimeAgo(*s.LastFailure, d.loc), uikit.ColorWarning))
	} else {
		out = append(out, row("Last failure", "none", uikit.ColorSuccess))
	}
	if s.UsualDuration != nil {
		out = append(out, row("Usual duration", uikit.FormatElapsed(*s.UsualDuration), uikit.ColorText))
	}
	if s.Usual != nil {
		// The median, like the run list's baseline, so one runaway run doesn't
		// skew what "usual" means.
		out = append(out,
			row("Usual CPU", uikit.FormatCPUTime(s.Usual.CPUMs), uikit.ColorText),
			row("Usual memory", config.FormatByteSize(s.Usual.MemBytes), uikit.ColorText))
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
func failureColor(failed int) color.Color {
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

// taskSourceLabel names where a promotable task's definition lives, in the
// narrow value column the detail panel allows.
//
// A cron-sourced task shows its crontab's basename rather than the full path: the
// operator needs to know *which* crontab of the several they pointed at, and the
// leading /etc/cron.d/ is the part that clips. Staged tasks keep the relative
// staging path, which is short and always the same file.
func taskSourceLabel(source model.TaskSource, file string) string {
	if source != model.SourceCron {
		return config.StagingRelPath() + " (staged)"
	}
	if file == "" {
		return "a crontab (cron)"
	}
	return filepath.Base(file) + " (cron)"
}

func paramNames(params []model.TaskParam) []string {
	names := make([]string, len(params))
	for i := range params {
		names[i] = params[i].Key
	}
	return names
}

// taskDetailRow renders a "label  value" line with a fixed label column. The
// value is clipped to whatever width remains so a long path or dependency list
// can't wrap and break the modal box.
func taskDetailRow(label, value string, valueColor color.Color, labelCol, innerWidth int) string {
	labelCell := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextMuted).
		Width(labelCol).
		Render(label)
	value = uikit.TruncateToWidth(value, innerWidth-labelCol)
	valueCell := uikit.OnBg(uikit.ColorBgLight, valueColor).
		Render(value)
	return uikit.PadLine(labelCell+valueCell, innerWidth, uikit.ColorBgLight)
}
