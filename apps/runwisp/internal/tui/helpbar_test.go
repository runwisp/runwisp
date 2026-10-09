// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestHelpBarFit(t *testing.T) {
	bar := helpBar{}.
		add(prioNav, "↑↓ navigate", "enter open").
		add(prioAction, "r restart", "s stop").
		add(prioNav, "f filter").
		add(prioQuit, "q/^C quit").
		add(prioHelp, "? help")
	full := "↑↓ navigate  enter open  r restart  s stop  f filter  q/^C quit  ? help"

	for _, tt := range []struct {
		name  string
		width int
		want  string
	}{
		{"fits as is", lipgloss.Width(full), full},
		{"quit goes first", lipgloss.Width(full) - 1, "↑↓ navigate  enter open  r restart  s stop  f filter  ? help"},
		{"then nav, rightmost first", 49, "↑↓ navigate  r restart  s stop  ? help"},
		{"then actions, keeping help", 20, "r restart  ? help"},
		{"too narrow for anything but help", 3, "? help"},
		{"no width at all", -2, "? help"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := bar.fit(tt.width)
			assert.Equal(t, tt.want, got)
			if tt.width >= lipgloss.Width("? help") {
				assert.LessOrEqual(t, lipgloss.Width(got), tt.width)
			}
		})
	}
}

// A service's help bar used to run past 80 columns, cutting off `? help`, the
// one hint that leads to every other key.
func TestRenderHelpBar_FitsTerminalWidth(t *testing.T) {
	tasks := []model.Task{{Name: "web", Kind: model.KindService, ManualTrigger: true}}
	for _, focusMain := range []bool{false, true} {
		for _, width := range []int{60, 80, 120} {
			m := newTestModel(tasks)
			m.handleWindowSize(tea.WindowSizeMsg{Width: width, Height: 30})
			selectSidebarItem(&m, 1)
			if focusMain {
				m.focusMainPanel()
			}
			got := m.renderHelpBar()
			plain := ansi.Strip(got)
			assert.LessOrEqual(t, lipgloss.Width(got), width, plain)
			assert.Contains(t, plain, "? help", "width %d", width)
			assert.Contains(t, plain, "s stop", "width %d", width)
			assert.Contains(t, plain, "r restart", "width %d", width)
		}
	}
}
