// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"sort"

	"log/slog"

	"github.com/runwisp/runwisp/internal/model"
)

// RunOnStartResult summarises run_on_start firings performed at daemon boot.
type RunOnStartResult struct {
	Triggered int
	Errors    int
}

// RunStartupTasks fires every task with run_on_start=true exactly once, at
// daemon boot. It is the @reboot equivalent: independent of cron and catch-up,
// and not subject to max_catch_up_runs. A run_on_start task with no cron still
// fires here; one with a cron fires here in addition to its schedule.
//
// Services are skipped — they already start every instance at boot. Tasks are
// visited in name order so the firing sequence is deterministic; the function
// reads no clock, filesystem, or randomness.
func RunStartupTasks(tasks map[string]*model.Task, runner TaskRunner) RunOnStartResult {
	var result RunOnStartResult
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		task := tasks[name]
		if !task.RunOnStart || task.Kind.IsService() {
			continue
		}
		if _, err := runner.TriggerRunWithOptions(name, TriggerRunOptions{
			TriggeredBy: model.TriggeredByStartup,
		}); err != nil {
			slog.Error("Failed to fire run_on_start task", "task", name, "err", err)
			result.Errors++
			continue
		}
		result.Triggered++
	}
	return result
}
