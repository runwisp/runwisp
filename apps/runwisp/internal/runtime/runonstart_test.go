// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"errors"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunStartupTasks(t *testing.T) {
	tasks := map[string]*model.Task{
		"boot":      {Name: "boot", Kind: model.KindTask, RunOnStart: true, Run: "echo hi"},
		"scheduled": {Name: "scheduled", Kind: model.KindTask, RunOnStart: false, Cron: "* * * * *"},
		"svc":       {Name: "svc", Kind: model.KindService, RunOnStart: true}, // ignored on services
	}

	runner := &fakeTaskRunner{}

	result := RunStartupTasks(t.Context(), tasks, runner, nil, "", nil)

	assert.Equal(t, 1, result.Triggered)
	assert.Equal(t, 0, result.Errors)
	// Only the run_on_start task fires — never the scheduled task or the service.
	assert.Equal(t, []string{"boot"}, runner.triggers)
	assert.Equal(t, []TriggerRunOptions{{TriggeredBy: model.TriggeredByStartup}}, runner.triggerOpts)
}

func TestRunStartupTasksCountsErrors(t *testing.T) {
	tasks := map[string]*model.Task{
		"boot": {Name: "boot", Kind: model.KindTask, RunOnStart: true, Run: "echo hi"},
	}
	runner := &fakeTaskRunner{triggerErr: errors.New("boom")}

	result := RunStartupTasks(t.Context(), tasks, runner, nil, "", nil)

	assert.Equal(t, 0, result.Triggered)
	assert.Equal(t, 1, result.Errors)
}

// TestRunStartupTasksBootModeOncePerBoot is the RW-37 regression: an imported
// @reboot job (run_on_start = "boot") must not fire again on a restart,
// self-update re-exec, or takeover within one boot. Each daemon start is one
// RunStartupTasks call against the same database.
func TestRunStartupTasksBootModeOncePerBoot(t *testing.T) {
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	tasks := map[string]*model.Task{
		"reboot": {Name: "reboot", Kind: model.KindTask, Run: "echo up",
			RunOnStart: true, RunOnStartMode: model.RunOnStartBoot},
		"daemon": {Name: "daemon", Kind: model.KindTask, Run: "echo up",
			RunOnStart: true, RunOnStartMode: model.RunOnStartDaemon},
	}
	start := func(bootID string) []string {
		runner := &fakeTaskRunner{}
		RunStartupTasks(t.Context(), tasks, runner, db, bootID, nil)
		return runner.triggers
	}

	assert.Equal(t, []string{"daemon", "reboot"}, start("boot-1"), "first start of a boot fires both")
	assert.Equal(t, []string{"daemon"}, start("boot-1"), "a restart within the boot fires only the daemon-mode task")
	assert.Equal(t, []string{"daemon", "reboot"}, start("boot-2"), "a new boot fires the boot task again")
}

// A failed trigger must not record the boot, or the task would never run this
// boot and nothing would say so.
func TestRunStartupTasksBootModeFailedTriggerRetriesOnRestart(t *testing.T) {
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	tasks := map[string]*model.Task{
		"reboot": {Name: "reboot", Kind: model.KindTask, Run: "echo up",
			RunOnStart: true, RunOnStartMode: model.RunOnStartBoot},
	}

	failed := RunStartupTasks(t.Context(), tasks, &fakeTaskRunner{triggerErr: errors.New("boom")}, db, "boot-1", nil)
	assert.Equal(t, 1, failed.Errors)

	runner := &fakeTaskRunner{}
	RunStartupTasks(t.Context(), tasks, runner, db, "boot-1", nil)
	assert.Equal(t, []string{"reboot"}, runner.triggers)
}

// With no boot identity the boot task fires on every start and the store is
// never touched (the mock has no expectations, so any call fails).
func TestRunStartupTasksBootModeWithoutBootIDFiresEveryStart(t *testing.T) {
	tasks := map[string]*model.Task{
		"reboot": {Name: "reboot", Kind: model.KindTask, Run: "echo up",
			RunOnStart: true, RunOnStartMode: model.RunOnStartBoot},
	}
	db := new(testutil.MockRunRepository)
	for range 2 {
		runner := &fakeTaskRunner{}
		RunStartupTasks(t.Context(), tasks, runner, db, "", nil)
		assert.Equal(t, []string{"reboot"}, runner.triggers)
	}
	db.AssertExpectations(t)
}

// A paused schedule means no automatic runs at all: a restart (or self-update
// re-exec) must not fire run_on_start for a job the operator held back.
func TestRunStartupTasksSkipsPausedTask(t *testing.T) {
	tasks := map[string]*model.Task{
		"paused": {Name: "paused", Kind: model.KindTask, RunOnStart: true, Cron: "0 3 * * *", ManualTrigger: true},
		"boot":   {Name: "boot", Kind: model.KindTask, RunOnStart: true, Run: "echo hi"},
	}
	runner := &fakeTaskRunner{}

	result := RunStartupTasks(t.Context(), tasks, runner, nil, "", func(name string) bool { return name == "paused" })

	assert.Equal(t, 1, result.Triggered)
	assert.Equal(t, []string{"boot"}, runner.triggers)
	assert.Equal(t, []TriggerRunOptions{{TriggeredBy: model.TriggeredByStartup}}, runner.triggerOpts)
}
