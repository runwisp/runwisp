// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestScheduler(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := &model.Task{
		Name: "task1",
		Cron: "@every 1s",
		Run:  "echo hi",
	}
	jm.UpsertTask(task)
	tasks := map[string]*model.Task{"task1": task}

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	sched := NewScheduler(jm, tasks)
	_, err := sched.Start()
	assert.NoError(t, err)
	defer sched.Stop()

	// Wait for cron to trigger
	time.Sleep(1500 * time.Millisecond)

	exec.AssertCalled(t, "Execute", mock.Anything, task, mock.Anything)

	next := sched.GetNextRun("task1")
	assert.NotNil(t, next)
}
