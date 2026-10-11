// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

func TestResolveRestartChoice(t *testing.T) {
	cases := []struct {
		name, flag, input string
		isTTY             bool
		want              string
	}{
		{"flag wins over terminal", restartIdle, "later\n", true, restartIdle},
		{"no terminal never restarts", "", "", false, restartLater},
		{"enter means now", "", "\n", true, restartNow},
		{"i means idle", "", "i\n", true, restartIdle},
		{"l means later", "", "l\n", true, restartLater},
		{"closed stdin means now", "", "", true, restartNow},
	}
	for _, c := range cases {
		got, err := resolveRestartChoice(c.flag, strings.NewReader(c.input), io.Discard, c.isTTY, "v1.6.0")
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestActiveTaskRunsIgnoresServices(t *testing.T) {
	tasks := []model.TaskResponse{
		{Task: model.Task{Name: "backup", Kind: model.KindTask}},
		{Task: model.Task{Name: "web", Kind: model.KindService}},
	}
	runs := []model.Run{{TaskName: "web"}, {TaskName: "backup"}, {TaskName: "backup"}, {TaskName: "adhoc"}}
	if got := activeTaskRuns(tasks, runs); !slices.Equal(got, []string{"adhoc", "backup"}) {
		t.Fatalf("activeTaskRuns = %v, want [adhoc backup]", got)
	}
	if got := activeTaskRuns(tasks, []model.Run{{TaskName: "web"}}); len(got) != 0 {
		t.Fatalf("only a service running: got %v, want idle", got)
	}
}
