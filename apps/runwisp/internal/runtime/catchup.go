// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"fmt"
	"time"

	"log/slog"

	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/internal/cronspec"
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
// for cron ticks that were missed while the daemon was down. defaultLoc is the
// scheduler's resolved timezone ([scheduler] timezone); a task's own timezone
// overrides it, exactly as the live scheduler resolves it.
func RunMissedTickCatchUp(ctx context.Context, db storage.RunRepository, tasks map[string]*model.Task, runner TaskRunner, now time.Time, defaultLoc *time.Location) CatchUpResult {
	var result CatchUpResult
	parser := cronspec.NewScheduleParser()

	for _, task := range tasks {
		// Only the no-cron guard remains: detection is independent of the
		// re-run policy, so even catch_up = "skip" records and alerts on the
		// gap. Services (no cron) have no schedule to miss.
		if task.Cron == "" {
			continue
		}
		triggered, errors := catchupOneTask(ctx, db, parser, task, runner, now, defaultLoc)
		result.Triggered += triggered
		result.Errors += errors
	}

	return result
}

// catchupSchedule builds a task's schedule and the location its ticks are
// evaluated in, mirroring Scheduler.effectiveSpec: a per-task timezone is
// applied via a CRON_TZ= prefix (and used as the reference location), otherwise
// the scheduler default is used. Counting missed ticks in the wrong zone would
// mis-detect the downtime gap for any task not on the host's local time.
func catchupSchedule(parser cron.ScheduleParser, task *model.Task, defaultLoc *time.Location) (cron.Schedule, *time.Location, error) {
	spec := task.Cron
	loc := defaultLoc
	if loc == nil {
		loc = time.Local
	}
	if task.Timezone != "" {
		spec = "CRON_TZ=" + task.Timezone + " " + task.Cron
		if l, err := time.LoadLocation(task.Timezone); err == nil {
			loc = l
		}
	}
	schedule, err := parser.Parse(spec)
	if err != nil {
		return nil, nil, err
	}
	return schedule, loc, nil
}

// catchupOneTask processes a single task's catch-up logic and returns the
// number of runs triggered and errors encountered.
func catchupOneTask(ctx context.Context, db storage.RunRepository, parser cron.ScheduleParser, task *model.Task, runner TaskRunner, now time.Time, defaultLoc *time.Location) (triggered, errors int) {
	// Persist first-seen timestamp; INSERT OR IGNORE is a no-op on every
	// restart after the first. On first startup firstSeenAt == now, so
	// countMissedTicks returns 0 — no spurious initial run.
	if err := db.EnsureTaskRegistered(ctx, task.Name, now); err != nil {
		slog.Warn("Failed to register task for catch-up", "task", task.Name, "err", err)
		return 0, 1
	}

	schedule, loc, err := catchupSchedule(parser, task, defaultLoc)
	if err != nil {
		slog.Warn("Failed to parse schedule for catch-up", "task", task.Name, "err", err)
		return 0, 1
	}
	// Evaluate ticks in the task's effective zone. For the scheduler-default
	// case the schedule preserves the input location, so converting the anchor
	// and now here is what pins evaluation to defaultLoc.
	now = now.In(loc)

	anchor, ok, errCount := resolveCatchupAnchor(ctx, db, task)
	if !ok {
		return 0, errCount
	}
	anchor = anchor.In(loc)

	// Bound counting at max(cap, floor)+1: the +1 lets computeCatchupTriggers
	// still see missedCount > MaxCatchUpRuns (so all/latest/skip cap correctly)
	// no matter how high the operator set the cap, while the floor keeps the
	// reported gap honest for realistic backlogs.
	countCap := max(task.MaxCatchUpRuns, catchupCountDisplayFloor) + 1
	missedCount, lastTick, truncated := countMissedTicks(schedule, anchor, now, countCap)
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
		// DEBUG, not INFO: per-task catch-up detail is operator-visible via
		// --log-level=debug, but at the default INFO level the startup banner
		// already shows the total ("Triggered N catch-up runs for missed cron
		// ticks") — flooding stderr with one INFO per task fragments the
		// banner for no extra signal.
		slog.Debug("Recorded missed cron ticks",
			"task", task.Name,
			"missed", missedCount,
			"triggering", triggerCount,
			"policy", task.CatchUp,
		)
	}

	// Record one browsable terminal "missed" row per task per downtime gap and
	// raise a failure-level alert. This happens regardless of the re-run policy
	// (including skip, which triggers nothing) — the detected total is reported
	// even when MaxCatchUpRuns drops older ticks from the re-run. firstTick is
	// the first tick after the anchor; lastTick anchors the next restart.
	firstTick := schedule.Next(anchor)
	reason := missedRunReason(missedCount, firstTick, capped, triggerCount, truncated)
	if err := runner.RecordMissedRun(task.Name, lastTick, reason); err != nil {
		slog.Error("Failed to record missed run", "task", task.Name, "err", err)
		errors++
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
func resolveCatchupAnchor(ctx context.Context, db storage.RunRepository, task *model.Task) (time.Time, bool, int) {
	lastRun, err := db.GetLastRunByTask(ctx, task.Name)
	if err != nil {
		slog.Warn("Failed to query last run for catch-up", "task", task.Name, "err", err)
		return time.Time{}, false, 1
	}
	if lastRun != nil {
		return lastRun.CreatedAt, true, 0
	}
	reg, err := db.GetTaskRegistration(ctx, task.Name)
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
// count was capped by MaxCatchUpRuns. Detection is policy-independent (the
// caller always records a missed row); this governs only re-running:
//   - skip:   re-run nothing (the gap is alerted but never re-fired)
//   - latest: re-run only the most recent missed tick
//   - all:    re-run every missed tick, capped at MaxCatchUpRuns
func computeCatchupTriggers(task *model.Task, missedCount int) (triggers int, capped bool) {
	if task.CatchUp == model.MissedRunSkip {
		return 0, false
	}
	if task.CatchUp == model.MissedRunLatest {
		return 1, false
	}
	if task.CatchUp == model.MissedRunAll && missedCount > task.MaxCatchUpRuns {
		return task.MaxCatchUpRuns, true
	}
	return missedCount, false
}

// catchupCountDisplayFloor bounds how far countMissedTicks walks even when the
// re-run cap is small. A sub-minute schedule over a long outage would otherwise
// step millions of ticks one Next() call at a time. The floor keeps the
// reported gap size accurate for any realistic backlog while still bounding the
// work; beyond it the count is reported as "at least N+".
const catchupCountDisplayFloor = 1000

// countMissedTicks counts how many cron ticks fall strictly between lastRunTime
// and now, and returns the latest such tick (<= now). The tick at lastRunTime
// itself is not counted (it was already executed). lastTick is the zero time
// when count is 0. Counting stops once count reaches maxCount, returning
// truncated=true so callers can report the gap as "at least N+" rather than
// walking an unbounded per-second backlog.
func countMissedTicks(schedule cron.Schedule, lastRunTime, now time.Time, maxCount int) (count int, lastTick time.Time, truncated bool) {
	next := schedule.Next(lastRunTime)
	for !next.After(now) {
		count++
		lastTick = next
		if count >= maxCount {
			return count, lastTick, true
		}
		next = schedule.Next(next)
	}
	return count, lastTick, false
}

// missedRunReason builds the human sentence recorded on the missed run and
// surfaced as the notification body. since is the first missed tick; the count
// is the detected total even when MaxCatchUpRuns capped the re-run, so the
// operator sees the true size of the gap. When counting was truncated (a huge
// sub-minute backlog), the count is reported as "at least N+" rather than
// understated. When capped, it notes how many of the backlog were re-fired.
func missedRunReason(missedCount int, since time.Time, capped bool, triggered int, truncated bool) string {
	plural := "s"
	if missedCount == 1 {
		plural = ""
	}
	atLeast, plus := "", ""
	if truncated {
		atLeast, plus = "at least ", "+"
	}
	reason := fmt.Sprintf("%s%d%s scheduled run%s missed since %s (daemon was down)",
		atLeast, missedCount, plus, plural, since.Format("2006-01-02 15:04"))
	if capped {
		reason += fmt.Sprintf("; re-ran the most recent %d, older ticks dropped per max_catch_up_runs", triggered)
	}
	return reason
}
