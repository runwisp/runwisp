// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"maps"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// RunParamsDialog renders a centered, read-only modal listing a run's resolved
// parameters, one "key = value" per line. It mirrors the daemon's run history —
// it never *defines* or *edits* params (that's ParamFormDialog, shown on
// trigger); it only surfaces what a finished/running run was launched with.
type RunParamsDialog struct {
	taskName string
	lines    []string
}

// NewRunParamsDialog builds the dialog from a run's resolved param map, sorting
// by key so the list is stable across renders.
func NewRunParamsDialog(taskName string, params map[string]string) *RunParamsDialog {
	keys := slices.Sorted(maps.Keys(params))
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s = %s", k, params[k]))
	}
	return &RunParamsDialog{taskName: taskName, lines: lines}
}

// Update reports whether the dialog should close.
func (d *RunParamsDialog) Update(msg tea.Msg) (tea.Cmd, bool) {
	return nil, isTextModalDismiss(msg)
}

func (d *RunParamsDialog) View(screenWidth, screenHeight int) string {
	widest := 0
	for _, l := range d.lines {
		if n := len([]rune(l)); n > widest {
			widest = n
		}
	}
	dialogWidth, innerWidth := modalDimensions(screenWidth, widest+8, 44)

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Params · "+d.taskName, innerWidth, uikit.ColorTextBright, true),
		modalEmptyLine(innerWidth),
	}
	for _, l := range d.lines {
		lines = append(lines, modalLeftLine("  "+l, innerWidth, uikit.ColorText))
	}
	lines = append(lines, modalFooter("esc close", innerWidth)...)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorSecondary, lines)
	return box.view
}
