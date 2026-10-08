// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"log/slog"

	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
)

// CatchUpResult summarises missed-tick catch-up actions taken at startup.
type CatchUpResult struct {
	Triggered int
	Skipped   int
	Errors    int
}

// SnapshotCatchupAnchors resolves every schedulable task's catch-up anchor
// (registering its first-seen timestamp along the way) and returns a snapshot
// keyed by task name; a task absent from the result has no usable anchor
// (never seen before and its registration couldn't be read back) and must be
// skipped by RunMissedTickCatchUp. errs counts registration/lookup failures.
//
// tasks must be the whole config: registrations of tasks not in it are
// forgotten first, so a task that was removed and later comes back anchors at
// this boot instead of reporting its whole absence as missed.
//
// Call this before the scheduler starts and before RunStartupTasks fires: both
// can create a run for this boot, which would then masquerade as "the last run"
// and hide the downtime gap. RunMissedTickCatchUp runs later, once notify has
// subscribed, so the alert is not lost.
func SnapshotCatchupAnchors(ctx context.Context, db storage.RunRepository, tasks map[string]*model.Task, now time.Time) (anchors map[string]time.Time, errs int) {
	anchors = make(map[string]time.Time, len(tasks))
	if err := db.ForgetTaskRegistrationsExcept(ctx, slices.Collect(maps.Keys(tasks))); err != nil {
		slog.Warn("Failed to forget registrations of removed tasks", "err", err)
		errs++
	}
	for _, task := range tasks {
		// Detection ignores the re-run policy, so even catch_up = 0 records and
		// alerts on the gap. Only tasks whose schedule is ours count: a service
		// has none, and a held task's ticks belong to whatever is holding it.
		//
		// Skipping a held task also skips EnsureTaskRegistered, so it gets no
		// first-seen anchor until it becomes schedulable. Anchoring while held
		// would page the operator for every tick cron ran perfectly well.
		if !task.Schedulable() {
			continue
		}
		// Persist first-seen timestamp; INSERT OR IGNORE is a no-op on every
		// restart after the first. On first startup firstSeenAt == now, so
		// countMissedTicks returns 0 — no spurious initial run.
		if err := db.EnsureTaskRegistered(ctx, task.Name, now); err != nil {
			slog.Warn("Failed to register task for catch-up", "task", task.Name, "err", err)
			errs++
			continue
		}
		at, ok, errCount := resolveCatchupAnchor(ctx, db, task)
		errs += errCount
		if ok {
			anchors[task.Name] = at
		}
	}
	return anchors, errs
}

// RunMissedTickCatchUp triggers catch-up runs for cron ticks that were missed
// while the daemon was down, for every task with an entry in anchors (from a
// prior SnapshotCatchupAnchors call — a task with no entry is skipped).
// defaultLoc is the scheduler's resolved timezone ([daemon] timezone); a
// task's own timezone overrides it, exactly as the live scheduler resolves it.
// snapshotErrors seeds the result so registration/lookup failures from the
// snapshot phase are still reflected in the total.
func RunMissedTickCatchUp(tasks map[string]*model.Task, runner TaskRunner, now time.Time, defaultLoc *time.Location, anchors map[string]time.Time, snapshotErrors int) CatchUpResult {
	result := CatchUpResult{Errors: snapshotErrors}
	parser := cronspec.NewScheduleParser()

	for _, task := range tasks {
		if !task.Schedulable() {
			continue
		}
		anchor, ok := anchors[task.Name]
		if !ok {
			continue
		}
		triggered, errors := catchupOneTask(parser, task, runner, now, defaultLoc, anchor)
		result.Triggered += triggered
		result.Errors += errors
	}

	return result
}

// catchupSchedule builds a task's schedule and the location its ticks are
// evaluated in, via the same resolution the live scheduler uses
// (resolveTaskSchedule). Counting missed ticks in the wrong zone would
// mis-detect the downtime gap for any task not on the host's local time.
func catchupSchedule(parser cron.ScheduleParser, task *model.Task, defaultLoc *time.Location) (cron.Schedule, *time.Location, error) {
	spec, loc := resolveTaskSchedule(task, defaultLoc)
	schedule, err := parser.Parse(spec)
	if err != nil {
		return nil, nil, err
	}
	return schedule, loc, nil
}

// catchupOneTask processes a single task's catch-up logic and returns the
// number of runs triggered and errors encountered. anchor is the task's
// catch-up anchor, resolved earlier by SnapshotCatchupAnchors.
func catchupOneTask(parser cron.ScheduleParser, task *model.Task, runner TaskRunner, now time.Time, defaultLoc *time.Location, anchor time.Time) (triggered, errors int) {
	schedule, loc, err := catchupSchedule(parser, task, defaultLoc)
	if err != nil {
		slog.Warn("Failed to parse schedule for catch-up", "task", task.Name, "err", err)
		return 0, 1
	}
	// Evaluate ticks in the task's effective zone. For the scheduler-default
	// case the schedule preserves the input location, so converting the anchor
	// and now here is what pins evaluation to defaultLoc.
	now = now.In(loc)
	anchor = anchor.In(loc)

	// Bound counting at max(cap, floor)+1: the +1 lets computeCatchupTriggers
	// still see missedCount > catch_up (so the cap applies correctly) no matter
	// how high the operator set catch_up, while the floor keeps the reported gap
	// honest for realistic backlogs.
	countCap := max(task.CatchUpValue(), catchupCountDisplayFloor) + 1
	missedCount, lastTick, truncated := countMissedTicks(schedule, anchor, now, countCap)
	if missedCount == 0 {
		return 0, 0
	}

	triggerCount, capped := computeCatchupTriggers(task, missedCount)
	if capped {
		slog.Warn("Catch-up backlog exceeded catch_up; dropping older missed ticks",
			"task", task.Name,
			"missed", missedCount,
			"catch_up", task.CatchUpValue(),
			"triggering", triggerCount,
			"dropped", missedCount-triggerCount,
		)
	} else {
		// DEBUG, not INFO: the startup banner already reports the total.
		slog.Debug("Recorded missed cron ticks",
			"task", task.Name,
			"missed", missedCount,
			"triggering", triggerCount,
			"catch_up", task.CatchUpValue(),
		)
	}

	// Record one browsable terminal "missed" row per task per downtime gap and
	// raise a failure-level alert, regardless of the re-run policy. The detected
	// total is reported even when catch_up drops older ticks from the re-run.
	firstTick := schedule.Next(anchor)
	// The recorded row's CreatedAt anchors the next restart's counting. When
	// counting was truncated, lastTick is only the maxCount-th tick, so anchor
	// at now instead: otherwise the next restart re-counts and re-alerts this
	// gap, which is already reported as "at least N+".
	anchorTick := lastTick
	if truncated {
		anchorTick = now
	}
	reason := missedRunReason(missedCount, firstTick, capped, triggerCount, truncated)
	if err := runner.RecordMissedRun(task.Name, anchorTick, reason); err != nil {
		slog.Error("Failed to record missed run", "task", task.Name, "err", err)
		errors++
	}

	for range triggerCount {
		if _, err := runner.TriggerRunWithOptions(task.Name, TriggerRunOptions{TriggeredBy: model.TriggeredByCron}); err != nil {
			slog.Error("Failed to trigger catch-up run", "task", task.Name, "err", err)
			errors++
		} else {
			triggered++
		}
	}
	return triggered, errors
}

// resolveCatchupAnchor returns the time to use as the catch-up anchor point:
// the latest of the task's first-seen time, its last run (remembered on the
// registration even after retention deletes the row), and its last schedule
// resume (a paused window is the operator's choice, not missed ticks). A task
// whose schedule is paused right now owes nothing and is skipped, as is one
// with no registration.
// Returns (anchor, true, 0) on success or (zero, false, 1) on error/skip.
func resolveCatchupAnchor(ctx context.Context, db storage.RunRepository, task *model.Task) (time.Time, bool, int) {
	reg, err := db.GetTaskRegistration(ctx, task.Name)
	if err != nil {
		slog.Warn("Failed to query task registration for catch-up", "task", task.Name, "err", err)
		return time.Time{}, false, 1
	}
	if reg == nil || reg.PausedAt != nil {
		return time.Time{}, false, 0
	}
	anchor := reg.FirstSeenAt
	for _, floor := range []*time.Time{reg.LastRunAt, reg.ResumedAt} {
		if floor != nil && floor.After(anchor) {
			anchor = *floor
		}
	}
	return anchor, true, 0
}

// computeCatchupTriggers returns the number of runs to trigger and whether older
// ticks were dropped by the catch_up cap. Detection is cap-independent (the
// caller always records a missed row); this governs only re-running. catch_up is
// the max ticks to re-fire: 0 re-runs nothing (the gap is still alerted), 1
// re-runs only the most recent, N re-runs the N most recent. capped is true only
// when the backlog exceeded a nonzero cap (so some ticks were re-fired and older
// ones dropped) — it drives the warning log and the reason suffix.
func computeCatchupTriggers(task *model.Task, missedCount int) (triggers int, capped bool) {
	catchUpCap := task.CatchUpValue()
	if missedCount > catchUpCap {
		return catchUpCap, catchUpCap > 0
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
//
// A tick whose wall-clock reading (in lastRunTime's location) repeats one
// already seen within the same wall-hour is a DST fall-back duplicate, which
// fireOnce suppresses live as ReasonDSTSkipped. It would never have run, so it
// is walked past without being counted or becoming lastTick. The rewound hour
// replays every tick in it (02:00 CEST, 02:30 CEST, 02:00 CET, 02:30 CET), so a
// duplicate is not necessarily adjacent. This mirrors fireOnce's firedHour
// exactly so catch-up and the live scheduler agree. Fixed-interval (@every)
// schedules are exempt, as in fireOnce (see isFixedInterval).
func countMissedTicks(schedule cron.Schedule, lastRunTime, now time.Time, maxCount int) (count int, lastTick time.Time, truncated bool) {
	next := schedule.Next(lastRunTime)
	fixedInterval := isFixedInterval(schedule)
	var curHour wallHour
	var seen map[wallSecond]struct{}
	for !next.After(now) {
		if !fixedInterval {
			wall := newWallSecond(next)
			hour := wall.inHour()
			if seen == nil || hour != curHour {
				curHour = hour
				seen = make(map[wallSecond]struct{})
			}
			if _, duplicate := seen[wall]; duplicate {
				next = schedule.Next(next)
				continue
			}
			seen[wall] = struct{}{}
		}

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
// is the detected total even when catch_up capped the re-run, so the
// operator sees the true size of the gap. When counting was truncated (a huge
// sub-minute backlog), the count is reported as "at least N+" rather than
// understated. When capped, it notes how many of the backlog were re-fired.
func missedRunReason(missedCount int, since time.Time, capped bool, triggered int, truncated bool) string {
	atLeast, plus := "", ""
	if truncated {
		atLeast, plus = "at least ", "+"
	}
	reason := fmt.Sprintf("%s%d%s scheduled run%s missed since %s (daemon was down)",
		atLeast, missedCount, plus, textutil.Pluralize(missedCount, "", "s"), since.Format("2006-01-02 15:04"))
	if capped {
		reason += fmt.Sprintf("; re-ran the most recent %d, older ticks dropped per catch_up", triggered)
	}
	return reason
}
