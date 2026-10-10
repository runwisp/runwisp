// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

func TestLastRuns_KeepsNewestAndTakesUpdates(t *testing.T) {
	started := new(time.Now())
	l := lastRuns{}
	l.observe(model.Run{ID: "02", TaskName: "a", StartedAt: started, IsFailure: true})
	l.observe(model.Run{ID: "01", TaskName: "a", StartedAt: started}) // older event arrives late
	if !l["a"].IsFailure {
		t.Fatal("an older run must not replace the newest")
	}
	l.observe(model.Run{ID: "03", TaskName: "a", Status: model.PhaseEnded}) // skipped: never started
	if !l["a"].IsFailure {
		t.Fatal("a run that never started must not hide the last real one")
	}
	l.observe(model.Run{ID: "02", TaskName: "a", StartedAt: started}) // same run, updated
	if l["a"].IsFailure {
		t.Fatal("an update to the newest run must replace it")
	}
}

// TestTaskMarks_FollowTaskStatePoll: the /api/tasks lastRun is the source of
// truth. A poll replaces what the TUI knew, so a deleted failure clears its
// mark and a missed end event can't leave a task marked running; loaded run
// list pages don't feed the marks at all.
func TestTaskMarks_FollowTaskStatePoll(t *testing.T) {
	m := newTestModel([]model.Task{{Name: "a"}, {Name: "b"}})
	poll := func(runs map[string]model.Run) {
		updated, _ := m.Update(uikit.TaskStateMsg{LastRuns: runs})
		m = updated.(Model)
	}

	poll(map[string]model.Run{
		"a": {ID: "1", TaskName: "a", Status: model.PhaseEnded, IsFailure: true},
		"b": {ID: "2", TaskName: "b", Status: model.PhaseRunning},
	})
	marks := m.taskMarks()
	if marks["a"] != uikit.MarkFailed || marks["b"] != uikit.MarkRunning {
		t.Fatalf("poll marks not applied: %v", marks)
	}

	// The failed run was deleted and b's end event was missed.
	poll(map[string]model.Run{"b": {ID: "2", TaskName: "b", Status: model.PhaseEnded}})
	if marks := m.taskMarks(); len(marks) != 0 {
		t.Fatalf("a fresh poll must clear stale marks, got %v", marks)
	}

	// An older failure on a loaded (say failed-only filtered) page is not the
	// last run.
	m.handleExecWindowFetched(uikit.ExecWindowFetchedMsg{Items: []uikit.ExecListItem{
		{Run: model.Run{ID: "0", TaskName: "b", Status: model.PhaseEnded, IsFailure: true}},
	}})
	if _, ok := m.taskMarks()["b"]; ok {
		t.Fatal("a run on a loaded page must not set a mark")
	}
}

func TestTaskMarks_Precedence(t *testing.T) {
	tasks := []model.Task{{Name: "run"}, {Name: "fail"}, {Name: "stop"}, {Name: "pause"}, {Name: "ok"}, {Name: "runfail"}}
	m := newTestModel(tasks)
	m.info.TaskUsage = map[string]model.ResourceUsage{"runfail": {}}
	m.info.StoppedServices = map[string]bool{"stop": true, "fail": true}
	m.info.PausedTasks = map[string]time.Time{"pause": {}, "stop": {}}
	m.lastRuns = lastRuns{
		"run":     {ID: "1", TaskName: "run", Status: model.PhaseRunning},
		"fail":    {ID: "2", TaskName: "fail", Status: model.PhaseEnded, IsFailure: true},
		"runfail": {ID: "3", TaskName: "runfail", Status: model.PhaseEnded, IsFailure: true},
		"ok":      {ID: "4", TaskName: "ok", Status: model.PhaseEnded},
	}

	got := m.taskMarks()
	want := map[string]uikit.TaskMark{
		"run":     uikit.MarkRunning,
		"runfail": uikit.MarkRunning, // running outranks a failed last run
		"fail":    uikit.MarkFailed,  // a failure outranks a stopped service
		"stop":    uikit.MarkStopped, // stopped outranks paused
		"pause":   uikit.MarkPaused,
	}
	for name, mark := range want {
		if got[name] != mark {
			t.Errorf("%s: want %q, got %q", name, mark.Glyph, got[name].Glyph)
		}
	}
	if _, ok := got["ok"]; ok {
		t.Errorf("a healthy idle task should carry no mark, got %q", got["ok"].Glyph)
	}

	// The mark lands at the right edge of the task's sidebar row.
	m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		side := []rune(line)[:uikit.SidebarWidth]
		if strings.Contains(string(side), " fail ") {
			if strings.TrimSpace(string(side[len(side)-2:])) != uikit.MarkFailed.Glyph {
				t.Fatalf("expected the failed mark at the sidebar row end: %q", string(side))
			}
			return
		}
	}
	t.Fatal("task row not found in the sidebar")
}
