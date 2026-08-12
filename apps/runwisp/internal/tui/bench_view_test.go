// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

func benchModel(nRuns int) Model {
	tasks := []model.TaskBrief{{Name: "backup-postgres"}, {Name: "web"}, {Name: "cleanup"}}
	m := newTestModel(tasks)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 45})
	m = nm.(Model)

	items := make([]uikit.ExecListItem, nRuns)
	base := time.Now()
	for i := range items {
		items[i] = uikit.ExecListItem{
			Run: model.Run{
				ID:        "01J000000000000000000000AB",
				TaskName:  "backup-postgres",
				Status:    model.PhaseEnded,
				CreatedAt: base.Add(-time.Duration(i) * time.Minute),
			},
			Duration: "1.2s",
			TimeAgo:  "3m ago",
		}
	}
	m.execWindow.ApplyFetch(items, 0, nRuns)
	return m
}

// BenchmarkFullView guards the per-frame render cost — the work Bubble Tea runs
// on every message. Bubble Tea flushes to the terminal at 60fps, so this must
// stay comfortably under ~16ms; a regression here is what makes scrolling lag.
func BenchmarkFullView(b *testing.B) {
	m := benchModel(500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
