// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"slices"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

func TestBusyTasksIgnoresServices(t *testing.T) {
	tasks := []model.TaskResponse{
		{Task: model.Task{Name: "backup", Kind: model.KindTask}},
		{Task: model.Task{Name: "web", Kind: model.KindService}},
	}
	runs := []model.Run{{TaskName: "web"}, {TaskName: "backup"}, {TaskName: "backup"}, {TaskName: "adhoc"}}
	if got := busyTasks(tasks, runs); !slices.Equal(got, []string{"adhoc", "backup"}) {
		t.Fatalf("busyTasks = %v, want [adhoc backup]", got)
	}
	if got := busyTasks(tasks, []model.Run{{TaskName: "web"}}); len(got) != 0 {
		t.Fatalf("only a service running: got %v, want idle", got)
	}
}
