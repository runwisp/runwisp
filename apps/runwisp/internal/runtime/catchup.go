// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"time"

	"github.com/charmbracelet/log"
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
		if task.Trigger.Cron == "" || task.Trigger.Catchup == model.MissedRunNone {
			continue
		}

		// Persist first-seen timestamp; INSERT OR IGNORE is a no-op on every
		// restart after the first. On first startup firstSeenAt == now, so
		// countMissedTicks returns 0 — no spurious initial run.
		if err := db.EnsureTaskRegistered(task.Name, now); err != nil {
			log.Warn("Failed to register task for catch-up", "task", task.Name, "err", err)
			result.Errors++
			continue
		}

		schedule, err := parser.Parse(task.Trigger.Cron)
		if err != nil {
			log.Warn("Failed to parse schedule for catch-up", "task", task.Name, "err", err)
			result.Errors++
			continue
		}

		lastRun, err := db.GetLastRunByTask(task.Name)
		if err != nil {
			log.Warn("Failed to query last run for catch-up", "task", task.Name, "err", err)
			result.Errors++
			continue
		}

		var anchor time.Time
		if lastRun == nil {
			reg, err := db.GetTaskRegistration(task.Name)
			if err != nil {
				log.Warn("Failed to query task registration for catch-up", "task", task.Name, "err", err)
				result.Errors++
				continue
			}
			if reg == nil {
				continue
			}
			anchor = reg.FirstSeenAt
		} else {
			anchor = lastRun.CreatedAt
		}

		missedCount := countMissedTicks(schedule, anchor, now)
		if missedCount == 0 {
			continue
		}

		triggers := missedCount
		if task.Trigger.Catchup == model.MissedRunLatest {
			triggers = 1
		}

		log.Info("Triggering missed run catch-up",
			"task", task.Name,
			"missed", missedCount,
			"triggering", triggers,
			"policy", task.Trigger.Catchup,
		)

		for range triggers {
			if _, err := runner.TriggerRun(task.Name, model.TriggeredByCron); err != nil {
				log.Error("Failed to trigger catch-up run", "task", task.Name, "err", err)
				result.Errors++
			} else {
				result.Triggered++
			}
		}
	}

	return result
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
