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
	l := lastRuns{}
	l.observe(model.Run{ID: "02", TaskName: "a", IsFailure: true})
	l.observe(model.Run{ID: "01", TaskName: "a"}) // older page arrives late
	if !l["a"].IsFailure {
		t.Fatal("an older run must not replace the newest")
	}
	l.observe(model.Run{ID: "02", TaskName: "a"}) // same run, updated
	if l["a"].IsFailure {
		t.Fatal("an update to the newest run must replace it")
	}
}

func TestTaskMarks_Precedence(t *testing.T) {
	tasks := []model.Task{{Name: "run"}, {Name: "fail"}, {Name: "stop"}, {Name: "pause"}, {Name: "ok"}, {Name: "runfail"}}
	m := newTestModel(tasks)
	m.info.TaskUsage = map[string]model.ResourceUsage{"runfail": {}}
	m.info.StoppedServices = map[string]bool{"stop": true, "fail": true}
	m.info.PausedTasks = map[string]time.Time{"pause": {}, "stop": {}}
	m.lastRuns.observe(model.Run{ID: "1", TaskName: "run", Status: model.PhaseRunning})
	m.lastRuns.observe(model.Run{ID: "2", TaskName: "fail", Status: model.PhaseEnded, IsFailure: true})
	m.lastRuns.observe(model.Run{ID: "3", TaskName: "runfail", Status: model.PhaseEnded, IsFailure: true})
	m.lastRuns.observe(model.Run{ID: "4", TaskName: "ok", Status: model.PhaseEnded})

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
