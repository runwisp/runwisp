// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"time"

	"log/slog"

	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

// CatchUpResult summarises missed-tick catch-up actions taken at startup.
type CatchUpResult struct {
	Triggered int
	Skipped   int
	Errors    int
}

// RunMissedTickCatchUp inspects each scheduled task and triggers catch-up runs
// for cron ticks that were missed while the daemon was down.
func RunMissedTickCatchUp(db storage.RunRepository, tasks map[string]*model.Task, runner TaskRunner, now time.Time) CatchUpResult {
	var result CatchUpResult
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

	for _, task := range tasks {
		if task.Cron == "" || task.CatchUp == model.MissedRunSkip {
			continue
		}
		triggered, errors := catchupOneTask(db, parser, task, runner, now)
		result.Triggered += triggered
		result.Errors += errors
	}

	return result
}

// catchupOneTask processes a single task's catch-up logic and returns the
// number of runs triggered and errors encountered.
func catchupOneTask(db storage.RunRepository, parser cron.Parser, task *model.Task, runner TaskRunner, now time.Time) (triggered, errors int) {
	// Persist first-seen timestamp; INSERT OR IGNORE is a no-op on every
	// restart after the first. On first startup firstSeenAt == now, so
	// countMissedTicks returns 0 — no spurious initial run.
	if err := db.EnsureTaskRegistered(task.Name, now); err != nil {
		slog.Warn("Failed to register task for catch-up", "task", task.Name, "err", err)
		return 0, 1
	}

	schedule, err := parser.Parse(task.Cron)
	if err != nil {
		slog.Warn("Failed to parse schedule for catch-up", "task", task.Name, "err", err)
		return 0, 1
	}

	anchor, ok, errCount := resolveCatchupAnchor(db, task)
	if !ok {
		return 0, errCount
	}

	missedCount := countMissedTicks(schedule, anchor, now)
	if missedCount == 0 {
		return 0, 0
	}

	triggerCount, capped := computeCatchupTriggers(task, missedCount)
	if capped {
		slog.Warn("Catch-up backlog exceeded max_catch_up_runs; dropping older missed ticks",
			"task", task.Name,
			"missed", missedCount,
			"max_catch_up_runs", task.MaxCatchUpRuns,
			"triggering", triggerCount,
			"dropped", missedCount-triggerCount,
			"policy", task.CatchUp,
		)
	} else {
		slog.Info("Triggering missed run catch-up",
			"task", task.Name,
			"missed", missedCount,
			"triggering", triggerCount,
			"policy", task.CatchUp,
		)
	}

	for range triggerCount {
		if _, err := runner.TriggerRun(task.Name, model.TriggeredByCron); err != nil {
			slog.Error("Failed to trigger catch-up run", "task", task.Name, "err", err)
			errors++
		} else {
			triggered++
		}
	}
	return triggered, errors
}

// resolveCatchupAnchor returns the time to use as the catch-up anchor point
// (the last run time, or the first-seen registration time if no runs exist).
// Returns (anchor, true, 0) on success or (zero, false, 1) on error/skip.
func resolveCatchupAnchor(db storage.RunRepository, task *model.Task) (time.Time, bool, int) {
	lastRun, err := db.GetLastRunByTask(task.Name)
	if err != nil {
		slog.Warn("Failed to query last run for catch-up", "task", task.Name, "err", err)
		return time.Time{}, false, 1
	}
	if lastRun != nil {
		return lastRun.CreatedAt, true, 0
	}
	reg, err := db.GetTaskRegistration(task.Name)
	if err != nil {
		slog.Warn("Failed to query task registration for catch-up", "task", task.Name, "err", err)
		return time.Time{}, false, 1
	}
	if reg == nil {
		return time.Time{}, false, 0
	}
	return reg.FirstSeenAt, true, 0
}

// computeCatchupTriggers returns the number of runs to trigger and whether the
// count was capped by MaxCatchUpRuns.
func computeCatchupTriggers(task *model.Task, missedCount int) (triggers int, capped bool) {
	if task.CatchUp == model.MissedRunLatest {
		return 1, false
	}
	if task.CatchUp == model.MissedRunAll && missedCount > task.MaxCatchUpRuns {
		return task.MaxCatchUpRuns, true
	}
	return missedCount, false
}

// countMissedTicks counts how many cron ticks fall strictly between lastRunTime
// and now. The tick at lastRunTime itself is not counted (it was already executed).
func countMissedTicks(schedule cron.Schedule, lastRunTime, now time.Time) int {
	count := 0
	next := schedule.Next(lastRunTime)
	for !next.After(now) {
		count++
		next = schedule.Next(next)
	}
	return count
}
