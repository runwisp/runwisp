// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"errors"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestRunStartupTasks(t *testing.T) {
	tasks := map[string]*model.Task{
		"boot":      {Name: "boot", Kind: model.KindTask, RunOnStart: true, Run: "echo hi"},
		"scheduled": {Name: "scheduled", Kind: model.KindTask, RunOnStart: false, Cron: "* * * * *"},
		"svc":       {Name: "svc", Kind: model.KindService, RunOnStart: true}, // ignored on services
	}

	runner := new(mockTaskRunner)
	runner.On("TriggerRunWithOptions", "boot",
		TriggerRunOptions{TriggeredBy: model.TriggeredByStartup}).
		Return(&model.Run{}, nil).Once()

	result := RunStartupTasks(tasks, runner)

	assert.Equal(t, 1, result.Triggered)
	assert.Equal(t, 0, result.Errors)
	// Only the run_on_start task fires — never the scheduled task or the service.
	runner.AssertExpectations(t)
	runner.AssertNumberOfCalls(t, "TriggerRunWithOptions", 1)
}

func TestRunStartupTasksCountsErrors(t *testing.T) {
	tasks := map[string]*model.Task{
		"boot": {Name: "boot", Kind: model.KindTask, RunOnStart: true, Run: "echo hi"},
	}
	runner := new(mockTaskRunner)
	runner.On("TriggerRunWithOptions", "boot", mock.Anything).
		Return((*model.Run)(nil), errors.New("boom")).Once()

	result := RunStartupTasks(tasks, runner)

	assert.Equal(t, 0, result.Triggered)
	assert.Equal(t, 1, result.Errors)
}
