// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"image/color"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// RunDetailDialog is the on-demand run inspector — a centered modal that surfaces
// the per-run facts the exec view's thin header deliberately omits: exit code,
// retry lineage, trigger source, and instance index. Opened with `i` from inside
// an exec view so the log header stays uncluttered. When the run is a retry, the
// modal can jump to its parent run.
type RunDetailDialog struct {
	run           *model.Run
	isService     bool
	instanceCount int
}

// NewRunDetailDialog builds the inspector for a run. run is never nil here — the
// caller only opens the inspector when an exec view has a run.
func NewRunDetailDialog(run *model.Run, isService bool, instanceCount int) RunDetailDialog {
	return RunDetailDialog{run: run, isService: isService, instanceCount: instanceCount}
}

// ParentRef returns the task + run id of the run this one retried, and whether
// there is one. The exec interceptor uses it to open the parent on enter.
func (d *RunDetailDialog) ParentRef() (taskName, runID string, ok bool) {
	if d.run == nil || d.run.RetryOfRunID == nil {
		return "", "", false
	}
	return d.run.TaskName, *d.run.RetryOfRunID, true
}

// Update reports true when the dialog should close. Enter is handled by the
// interceptor (it may open the parent run), so it isn't a plain close key here.
func (d *RunDetailDialog) Update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "i", "esc", "q":
			return true
		}
	case tea.MouseClickMsg:
		return true
	}
	return false
}

func (d *RunDetailDialog) View(screenWidth, screenHeight int) string {
	const labelCol = 14
	dialogWidth, innerWidth := modalDimensions(screenWidth, 60, 44)
	run := d.run

	row := func(label, value string, color color.Color) string {
		return taskDetailRow(label, value, color, labelCol, innerWidth)
	}

	title := run.TaskName
	if d.instanceCount > 1 {
		title = fmt.Sprintf("%s #%d", run.TaskName, run.InstanceIndex+1)
	}

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine(title, innerWidth, uikit.ColorTextBright, true),
		modalEmptyLine(innerWidth),
	}
	lines = append(lines, d.facts(row)...)
	_, _, hasParent := d.ParentRef()
	footer := "esc close"
	if hasParent {
		footer = "enter open parent · esc close"
	}
	lines = append(lines,
		modalEmptyLine(innerWidth),
		modalSurfaceLine(footer, innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorPrimary, lines)
	return box.view
}

// facts renders the run's metadata rows, omitting any that don't apply (no exit
// code before the run ends, no retry lineage on a first attempt).
func (d *RunDetailDialog) facts(row func(label, value string, color color.Color) string) []string {
	run := d.run
	status := run.DisplayStatus()
	out := []string{
		row("Status", status, runStatusColor(status)),
		row("Run ID", run.ID, uikit.ColorText),
	}
	if run.Status == model.PhaseEnded {
		out = append(out, row("Exit code", strconv.Itoa(run.ExitCode), exitCodeColor(run.ExitCode)))
	}
	out = append(out, row("Triggered", string(run.TriggeredBy), uikit.ColorText))
	if d.isService || d.instanceCount > 1 {
		out = append(out, row("Instance", fmt.Sprintf("#%d", run.InstanceIndex+1), uikit.ColorText))
	}
	if run.RetryAttempt > 0 {
		out = append(out, row("Retry", fmt.Sprintf("attempt %d", run.RetryAttempt), uikit.ColorWarning))
	}
	if run.RetryOfRunID != nil {
		out = append(out, row("Retry of", *run.RetryOfRunID, uikit.ColorText))
	}
	started := run.StartedAt
	if started == nil {
		started = &run.CreatedAt
	}
	out = append(out, row("Started", formatRunTime(started), uikit.ColorText))
	if run.EndedAt != nil {
		out = append(out, row("Ended", formatRunTime(run.EndedAt), uikit.ColorText))
	}
	out = append(out, row("Duration", uikit.FormatDuration(*run), uikit.ColorText))
	if len(run.Params) > 0 {
		out = append(out, row("Params", strconv.Itoa(len(run.Params)), uikit.ColorTextMuted))
	}
	return out
}

// runStatusColor maps a display status to the row's value color, mirroring the
// run-list badge palette.
func runStatusColor(status string) color.Color {
	switch status {
	case "running":
		return uikit.ColorRunning
	case "succeeded":
		return uikit.ColorSuccess
	case "pending":
		return uikit.ColorPending
	case "failed", "crashed", "timeout", "start_failed", "log_overflow":
		return uikit.ColorError
	case "stopped", "missed":
		return uikit.ColorWarning
	default:
		return uikit.ColorText
	}
}

// exitCodeColor greens a clean exit and reddens any non-zero code.
func exitCodeColor(code int) color.Color {
	if code == 0 {
		return uikit.ColorSuccess
	}
	return uikit.ColorError
}

// formatRunTime renders a run timestamp in local time, or an em dash when unset.
func formatRunTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
