// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// lastRuns remembers the newest run seen per task, fed by run-list fetches and
// run events, so the sidebar can mark a task whose last run failed.
//
// ponytail: only tasks with a run in a loaded page or a live event are known,
// the same bound the web UI overview has; a server-side lastRun on the task
// list would make it exact.
type lastRuns map[string]model.Run

// observe records run when it's at least as new as the task's known last run.
// Run IDs are monotonic ULIDs, so ID order is creation order, and an update to
// the same run (running → ended) replaces it.
func (l lastRuns) observe(run model.Run) {
	if prev, ok := l[run.TaskName]; ok && prev.ID > run.ID {
		return
	}
	l[run.TaskName] = run
}

// taskMarks picks each task's sidebar status mark, in the web UI overview's
// precedence: running, then a failed last run, then a stopped service, then a
// paused schedule.
func (m *Model) taskMarks() map[string]uikit.TaskMark {
	marks := make(map[string]uikit.TaskMark, len(m.info.Tasks))
	for _, t := range m.info.Tasks {
		last, seen := m.lastRuns[t.Name]
		switch {
		case m.taskUsage(t.Name) != nil || (seen && last.Status == model.PhaseRunning):
			marks[t.Name] = uikit.MarkRunning
		case seen && last.IsFailure:
			marks[t.Name] = uikit.MarkFailed
		case m.info.StoppedServices[t.Name]:
			marks[t.Name] = uikit.MarkStopped
		case m.isPaused(t.Name):
			marks[t.Name] = uikit.MarkPaused
		}
	}
	return marks
}
