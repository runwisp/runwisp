// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"log/slog"

	cron "github.com/netresearch/go-cron"
	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime/jitter"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// ErrNotPausable is returned by Scheduler.Pause for a task whose cron schedule
// an operator can't pause; the wrapped message names the reason.
var ErrNotPausable = errors.New("schedule cannot be paused")

// ScheduleResult holds the outcome of scheduling tasks.
type ScheduleResult struct {
	Scheduled int
	// Held counts tasks that have a schedule but are owned by something else —
	// today, crontabs a live system cron daemon is still firing itself. They are
	// registered nowhere and fire on no clock, so boot has to report the count or
	// a box with every job held would look identical to one with nothing to do.
	Held     int
	Warnings []string
}

// Scheduler wraps go-cron to trigger tasks on a schedule. On fall-back
// DST days, when the wall clock revisits a minute, the second firing is
// suppressed and recorded with end_reason = "dst_skipped".
type Scheduler struct {
	cron        *cron.Cron
	location    *time.Location
	taskManager RunTrigger
	tasks       map[string]*model.Task
	entryIDs    map[string]cron.EntryID
	// firedTicks tracks, per task, every wall-clock second already fired within
	// the current wall-clock hour (in the task's effective TZ). A firing whose
	// tuple is already present is the DST fall-back duplicate to suppress. The
	// set is scoped to one wall hour — reset the moment the hour advances — so a
	// schedule with several ticks in the rewound hour ("0,30 1 * * *") dedups
	// every repeat, not just one, while staying bounded to an hour of ticks.
	firedTicks map[string]*firedHour
	// jitterPlans holds each jittered task's plan (see jitterPlan). Computed in
	// Start and rebuilt by RecomputeJitter when a reload changes the task set,
	// so a task lands at the same place every day between reloads. Tasks
	// without a jitter window are absent and fire immediately.
	jitterPlans map[string]jitterPlan
	// paused maps each task whose schedule an operator paused to when. A paused
	// task keeps its cron entry (so reloads, holds and jitter placement need no
	// special case); fireOnce drops its ticks and GetNextRun reports none.
	// pauses persists it; nil (tests that never call RestorePauses) keeps
	// pauses in memory only.
	paused  map[string]time.Time
	pauses  *storage.SQLiteDatabase
	now     func() time.Time
	mutex   sync.Mutex
	started bool
}

// jitterPlan is a task's resolved start-spread: a slot offset within its
// window, the window length used as the gate's free-check horizon, and the
// schedule that yields the live gap to the next tick, so both can be clamped
// per-fire on irregular-cadence schedules.
type jitterPlan struct {
	offset   time.Duration
	window   time.Duration
	schedule cron.Schedule
}

// wallSecond is a (date, hour, minute, second) tuple that lets us compare two
// firings as "the same wall-clock instant" without depending on UTC offsets,
// which is the whole point on DST fall-back days. Second granularity matters
// for 6-field specs: two sub-minute firings in the same minute (e.g. :00 and
// :30) differ in second and must not be treated as DST duplicates.
type wallSecond struct {
	year   int
	month  time.Month
	day    int
	hour   int
	minute int
	second int
}

func newWallSecond(t time.Time) wallSecond {
	return wallSecond{
		year:   t.Year(),
		month:  t.Month(),
		day:    t.Day(),
		hour:   t.Hour(),
		minute: t.Minute(),
		second: t.Second(),
	}
}

// wallHour is the (date, hour) prefix of a wallSecond — the scope over which
// DST fall-back can rewind and repeat wall-clock ticks.
type wallHour struct {
	year  int
	month time.Month
	day   int
	hour  int
}

func (w wallSecond) inHour() wallHour {
	return wallHour{year: w.year, month: w.month, day: w.day, hour: w.hour}
}

// firedHour is the set of wall-clock seconds already fired within one wall hour.
type firedHour struct {
	hour  wallHour
	ticks map[wallSecond]struct{}
}

// NewScheduler creates a scheduler. location controls how task cron expressions
// are interpreted; nil means UTC, which is the project default. clock overrides
// the scheduler's wall-clock reads; pass nil for time.Now — production always
// does, and tests inject a fake clock so DST-fall-back firing sequences are
// deterministic.
func NewScheduler(taskManager RunTrigger, tasks map[string]*model.Task, location *time.Location, clock func() time.Time) *Scheduler {
	if location == nil {
		location = time.UTC
	}
	if clock == nil {
		clock = time.Now
	}
	scheduler := &Scheduler{
		cron:        cron.New(cron.WithLocation(location), cron.WithParser(cronspec.NewParser())),
		location:    location,
		taskManager: taskManager,
		tasks:       tasks,
		entryIDs:    make(map[string]cron.EntryID),
		firedTicks:  make(map[string]*firedHour),
		jitterPlans: make(map[string]jitterPlan),
		paused:      make(map[string]time.Time),
		now:         clock,
	}
	return scheduler
}

func (scheduler *Scheduler) Start() (ScheduleResult, error) {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()

	if scheduler.started {
		return ScheduleResult{}, nil
	}

	result := ScheduleResult{}
	for _, task := range scheduler.tasks {
		if !task.Schedulable() {
			if task.Held() {
				result.Held++
			}
			continue
		}
		if err := scheduler.addTask(task); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("failed to schedule %s: %v", task.Name, err))
			continue
		}
		result.Scheduled++
	}

	scheduler.computeJitterPlans()

	scheduler.cron.Start()
	scheduler.started = true
	return result, nil
}

// computeJitterPlans levels every jittered task against the others on a 24-hour
// time-of-day dial and stores each task's resulting slot offset and window
// length. Called under scheduler.mutex, from Start before the cron loop begins
// and from RecomputeJitter after a reload. The dial positions come only from
// the injected clock and the task set, so the same TOML + clock yield the same
// slots.
//
// Every jittered task — including the one placed at offset 0 — gets a plan and
// joins the work-conserving gate, so the earliest-slot task holds its peers
// while it runs and the gate can pull the next one forward when it finishes.
//
// Coordination is over base mod 24h, so "0 3 * * *", "10 3 * * *", and a weekly
// "0 3 * * 1" all land near 03:00 on the dial and coordinate even though they
// fire on different absolute days — the dominant fixed-time-of-day batch case.
// Schedules that don't align to 24h (e.g. @every 7h) still spread within their
// own window but coordinate only approximately.
func (scheduler *Scheduler) computeJitterPlans() {
	// Reproject into the daemon timezone before calling Next: a task with no
	// per-task timezone parses to a schedule whose Location is time.Local, and
	// go-cron's SpecSchedule.Next then evaluates in the input time's own
	// Location, so a raw clock reading would use the host OS zone instead of
	// scheduler.location. Mirrors catchup.go's now.In(loc).
	now := scheduler.now().In(scheduler.location)
	var windows []jitter.Window
	schedules := make(map[string]cron.Schedule)
	lengths := make(map[string]time.Duration)

	for _, task := range scheduler.tasks {
		jitterWindow := task.JitterValue()
		if jitterWindow <= 0 || !task.Schedulable() {
			continue
		}
		spec, _ := scheduler.effectiveSpec(task)
		sched, err := cronspec.NewParser().Parse(spec)
		if err != nil {
			// Already surfaced as a scheduling warning by addTask; skip silently.
			continue
		}
		base := sched.Next(now)
		gap := sched.Next(base).Sub(base)
		length := min(jitterWindow, gap-time.Second)
		if length <= 0 {
			// Gap too small to spread in (e.g. a sub-minute cadence); the task
			// just fires immediately.
			continue
		}
		phase := time.Duration(base.UnixNano()) % (24 * time.Hour)
		windows = append(windows, jitter.Window{Name: task.Name, Phase: phase, Length: length})
		schedules[task.Name] = sched
		lengths[task.Name] = length
	}

	if len(windows) == 0 {
		return
	}

	for name, offset := range jitter.Place(windows) {
		scheduler.jitterPlans[name] = jitterPlan{
			offset:   offset,
			window:   lengths[name],
			schedule: schedules[name],
		}
	}
	slog.Info("Jitter: routing cron tasks through the work-conserving gate",
		"tasks", len(scheduler.jitterPlans))
}

// RecomputeJitter rebuilds every jittered task's start-spread plan against the
// given task set. The reconciler calls this after a reload changes the task set
// so added and rescheduled tasks get their spread without a restart. The dial
// coordinates all jittered tasks together, so this re-places the whole set; an
// unchanged task may shift within its window but never onto another tick.
func (scheduler *Scheduler) RecomputeJitter(tasks map[string]*model.Task) {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	scheduler.tasks = tasks
	scheduler.jitterPlans = make(map[string]jitterPlan)
	scheduler.computeJitterPlans()
}

// SetLocation re-bases the schedules that follow the daemon timezone, for a
// reload that changes [daemon] timezone. Only wall-clock tasks with no timezone
// of their own are re-added, with their DST dedup state dropped since it is
// wall-clock in the old zone; @every intervals and tasks that pin a timezone
// keep their entry and their next tick. tasks is the live task set, as for
// RecomputeJitter. Returns a warning per task that failed to re-schedule.
func (scheduler *Scheduler) SetLocation(location *time.Location, tasks map[string]*model.Task) []string {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()

	scheduler.location = location
	scheduler.tasks = tasks
	// One snapshot up front: cron.Entry takes a full snapshot through the run
	// loop per call, which would make this O(n²) under the scheduler mutex.
	schedules := make(map[cron.EntryID]cron.Schedule, len(scheduler.entryIDs))
	for _, entry := range scheduler.cron.Entries() {
		schedules[entry.ID] = entry.Schedule
	}
	var warnings []string
	for _, name := range slices.Sorted(maps.Keys(scheduler.entryIDs)) {
		entryID, task := scheduler.entryIDs[name], tasks[name]
		zoned, ok := schedules[entryID].(zonedSchedule)
		if task == nil || task.Timezone != "" || !ok || isFixedInterval(zoned.Schedule) {
			continue
		}
		scheduler.cron.Remove(entryID)
		delete(scheduler.entryIDs, name)
		delete(scheduler.firedTicks, name)
		if err := scheduler.addTask(task); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to schedule %s: %v", name, err))
		}
	}
	scheduler.jitterPlans = make(map[string]jitterPlan)
	scheduler.computeJitterPlans()
	return warnings
}

func (scheduler *Scheduler) Stop() {
	scheduler.mutex.Lock()
	if !scheduler.started {
		scheduler.mutex.Unlock()
		return
	}

	ctx := scheduler.cron.Stop()
	scheduler.started = false
	scheduler.mutex.Unlock()

	<-ctx.Done()
}

// AddTask schedules a single task after the scheduler has started, used by the
// reconciler when a reload adds a cron task. Tasks the scheduler doesn't own are
// ignored — a service has no schedule, and a held task's schedule belongs to
// something else (see model.Task.Schedulable). go-cron's AddFunc is safe to
// call on a running cron.
func (scheduler *Scheduler) AddTask(task *model.Task) error {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if !task.Schedulable() {
		return nil
	}
	return scheduler.addTask(task)
}

// RemoveTask unschedules a task by name. No-op when the task has no entry (e.g.
// a service, or a cron task that failed to schedule). cron.Remove is safe on a
// running cron. The DST dedup state is dropped alongside the entry so a later
// re-add starts clean.
func (scheduler *Scheduler) RemoveTask(name string) {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if entryID, ok := scheduler.entryIDs[name]; ok {
		scheduler.cron.Remove(entryID)
		delete(scheduler.entryIDs, name)
	}
	delete(scheduler.firedTicks, name)
	delete(scheduler.jitterPlans, name)
}

// effectiveSpec is resolveTaskSchedule against the scheduler's default location.
func (scheduler *Scheduler) effectiveSpec(task *model.Task) (string, *time.Location) {
	return resolveTaskSchedule(task, scheduler.location)
}

// resolveTaskSchedule builds the cron spec actually fed to the parser —
// task.Cron prefixed with CRON_TZ= when the task pins its own timezone — and
// the location used as the reference for wall-clock evaluation: the task's
// own timezone when set, else defaultLoc, else time.Local. Shared by the live
// scheduler (effectiveSpec) and startup catch-up (catchupSchedule) so both
// interpret a task's schedule identically.
func resolveTaskSchedule(task *model.Task, defaultLoc *time.Location) (string, *time.Location) {
	spec := task.Cron
	loc := defaultLoc
	if loc == nil {
		loc = time.Local
	}
	if task.Timezone != "" {
		// CRON_TZ= prefix is honored by go-cron's parser and
		// overrides the default location for this entry.
		spec = "CRON_TZ=" + task.Timezone + " " + task.Cron
		// Best-effort resolve: if it doesn't parse, the caller's parser.Parse
		// will fail and surface an error/warning. On success, use the task TZ
		// as the reference for wall-clock evaluation.
		if l, err := time.LoadLocation(task.Timezone); err == nil {
			loc = l
		}
	}
	return spec, loc
}

func (scheduler *Scheduler) addTask(task *model.Task) error {
	taskName := task.Name
	spec, loc := scheduler.effectiveSpec(task)
	// Parse here rather than via cron.AddFunc so the firing callback knows
	// whether it is on a fixed interval, which decides whether the DST
	// fall-back dedup applies at all. The parser is the one the cron itself was
	// built with, so the schedule is identical either way.
	schedule, err := cronspec.NewParser().Parse(spec)
	if err != nil {
		return err
	}
	fixedInterval := isFixedInterval(schedule)
	scheduler.entryIDs[taskName] = scheduler.cron.Schedule(zonedSchedule{schedule, loc}, cron.FuncJob(func() {
		scheduler.fireOnce(taskName, loc, fixedInterval)
	}))
	return nil
}

// zonedSchedule evaluates a schedule in loc whatever zone the caller's clock
// reads in. go-cron fixes its own location at construction, so pinning the
// zone per entry is what lets SetLocation re-base one entry without rebuilding
// the cron. A schedule with no timezone of its own otherwise follows the zone
// of the time handed to Next.
type zonedSchedule struct {
	cron.Schedule
	loc *time.Location
}

func (z zonedSchedule) Next(t time.Time) time.Time { return z.Schedule.Next(t.In(z.loc)) }

// isFixedInterval reports whether a schedule fires on a fixed duration
// (@every) instead of matching wall-clock fields. Such a schedule has no
// wall-clock time it can repeat: on a fall-back day every one of its firings
// in the rewound hour is a genuine tick of real elapsed time, even though an
// interval dividing an hour evenly ("@every 30m") lands each of them on a
// local time already seen an hour earlier. The wall-clock dedup must therefore
// skip them, mirroring the spring-forward exemption cronspec already makes for
// the same schedules.
func isFixedInterval(schedule cron.Schedule) bool {
	_, ok := schedule.(cron.ConstantDelaySchedule)
	return ok
}

// fireOnce is the cron callback for a scheduled task. It dedupes wall-clock
// duplicates (DST fall-back) by tracking every wall-clock second already
// fired within the task's current wall hour (see firedHour); a duplicate is
// recorded as ReasonDSTSkipped and never reaches the executor. fixedInterval
// tasks (@every) are exempt — see isFixedInterval.
func (scheduler *Scheduler) fireOnce(taskName string, loc *time.Location, fixedInterval bool) {
	// Runs in go-cron's own goroutine, which has no panic recovery: an
	// unguarded panic here would crash the process and (with a TUI attached)
	// leave the terminal in raw mode. Route it through the daemon's shutdown.
	defer crashguard.Guard()

	now := scheduler.now()
	nowLocal := now.In(loc)
	wm := newWallSecond(nowLocal)

	hour := wm.inHour()
	scheduler.mutex.Lock()
	if _, paused := scheduler.paused[taskName]; paused {
		scheduler.mutex.Unlock()
		slog.Debug("Skipped cron tick: schedule paused", "task", taskName)
		return
	}
	duplicate := false
	if !fixedInterval {
		fired, ok := scheduler.firedTicks[taskName]
		if !ok || fired.hour != hour {
			fired = &firedHour{hour: hour, ticks: make(map[wallSecond]struct{})}
			scheduler.firedTicks[taskName] = fired
		}
		_, duplicate = fired.ticks[wm]
		if !duplicate {
			fired.ticks[wm] = struct{}{}
		}
	}
	plan, hasJitter := scheduler.jitterPlans[taskName]
	scheduler.mutex.Unlock()

	if duplicate {
		slog.Info("Suppressed duplicate cron firing on DST fall-back",
			"task", taskName,
			"wall_clock", nowLocal.Format("2006-01-02 15:04:05 MST"),
		)
		if err := scheduler.taskManager.RecordSkippedFiring(taskName, model.ReasonDSTSkipped, model.TriggeredByCron); err != nil {
			slog.Error("Failed to record DST-skipped firing", "task", taskName, "err", err)
		}
		return
	}

	// Jitter applies only to genuine (non-DST-duplicate) firings. Clamp the
	// stored offset and window to just under the live gap (computed from now,
	// since the cron loop has already advanced entry.Next) so a misconfigured
	// window can never push a slot onto or past the next tick. The gate starts
	// the fire at min(when it frees, the slot). nowLocal, not now, feeds Next
	// for the same timezone reason as in computeJitterPlans.
	if hasJitter {
		gapLive := plan.schedule.Next(nowLocal).Sub(nowLocal)
		limit := max(gapLive-time.Second, 0)
		offset := min(plan.offset, limit)
		window := min(plan.window, limit)
		slot := now.Add(offset)
		slog.Debug("Cron submitting jittered task to gate", "name", taskName, "slot_offset", offset)
		scheduler.taskManager.ScheduleJitteredRun(taskName, now, slot, window)
		return
	}

	slog.Debug("Cron triggering task", "name", taskName)
	if _, err := scheduler.taskManager.TriggerRunWithOptions(taskName, TriggerRunOptions{TriggeredBy: model.TriggeredByCron}); err != nil {
		slog.Error("Failed to trigger task", "name", taskName, "err", err)
	}
}

// GetNextRun returns the next scheduled time for the task, if scheduled.
func (scheduler *Scheduler) GetNextRun(taskName string) *time.Time {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()

	entryID, ok := scheduler.entryIDs[taskName]
	if !ok {
		return nil
	}
	if _, paused := scheduler.paused[taskName]; paused {
		return nil
	}
	entry := scheduler.cron.Entry(entryID)
	// Surface the bare cron tick, even for jittered tasks: with no contention
	// the gate starts them at the tick, and the slot is only the latest they
	// could slip.
	next := entry.Next.In(scheduler.location)
	return &next
}

// pauseClearReason names why a task can't carry a schedule pause, or "" when it
// can. nil means the task is no longer defined.
func pauseClearReason(task *model.Task) string {
	switch {
	case task == nil:
		return "not in runwisp.toml"
	case task.Kind.IsService():
		return "it is a service"
	case task.Cron == "":
		return "it has no cron schedule"
	case !task.ManualTrigger:
		return "manual_trigger = false"
	}
	return ""
}

// RestorePauses loads the persisted schedule pauses at boot, before Start, and
// clears any the current config no longer allows (see PrunePauses). store
// becomes the write-through target for later Pause/Resume calls. The returned
// strings describe the cleared pauses, for the boot warnings.
func (scheduler *Scheduler) RestorePauses(ctx context.Context, store *storage.SQLiteDatabase) ([]string, error) {
	paused, err := store.ListPausedTaskSchedules(ctx)
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	scheduler.pauses = store
	if err != nil {
		return nil, err
	}
	scheduler.paused = paused
	cleared := scheduler.prunePausesLocked(ctx, scheduler.tasks)
	for name, at := range scheduler.paused {
		slog.Info("Cron schedule is paused", "task", name, "since", at)
	}
	return cleared, nil
}

// Pause stops the task's cron ticks from firing until Resume. The cron entry
// stays registered, so a reload that reschedules or re-plans the task keeps
// the pause. Pausing an already-paused task is a no-op that keeps the original
// pause time. Validated against the scheduler's own task set under its lock, so
// a reload racing the request can't slip a pause onto a task it just locked.
//
// ponytail: the single-row SQLite write runs under the scheduler lock; move it
// to a dedicated pause lock if it ever shows up in fire latency.
func (scheduler *Scheduler) Pause(ctx context.Context, name string) error {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	task := scheduler.tasks[name]
	if reason := pauseClearReason(task); reason != "" {
		return fmt.Errorf("%w: %s", ErrNotPausable, reason)
	}
	if task.Held() {
		return fmt.Errorf("%w: a system cron daemon still owns it", ErrNotPausable)
	}
	if _, ok := scheduler.paused[name]; ok {
		return nil
	}
	at := scheduler.now()
	if scheduler.pauses != nil {
		if err := scheduler.pauses.PauseTaskSchedule(ctx, name, at); err != nil {
			return fmt.Errorf("persist schedule pause: %w", err)
		}
	}
	scheduler.paused[name] = at
	slog.Info("Cron schedule paused", "task", name)
	return nil
}

// Resume lets the task's cron ticks fire again from the next tick on. Ticks
// that fell inside the pause are not caught up. A no-op when not paused.
func (scheduler *Scheduler) Resume(ctx context.Context, name string) error {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if _, ok := scheduler.paused[name]; !ok {
		return nil
	}
	if scheduler.pauses != nil {
		if err := scheduler.pauses.ResumeTaskSchedule(ctx, name, scheduler.now()); err != nil {
			return fmt.Errorf("persist schedule resume: %w", err)
		}
	}
	delete(scheduler.paused, name)
	slog.Info("Cron schedule resumed", "task", name)
	return nil
}

// PausedAt reports when the task's schedule was paused, or nil if it isn't.
func (scheduler *Scheduler) PausedAt(name string) *time.Time {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	at, ok := scheduler.paused[name]
	if !ok {
		return nil
	}
	return &at
}

// IsPaused reports whether the task's schedule is paused.
func (scheduler *Scheduler) IsPaused(name string) bool {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	_, ok := scheduler.paused[name]
	return ok
}

// PrunePauses clears every pause whose task the given (reloaded) task set no
// longer allows to be paused: removed, turned into a service, stripped of its
// cron, or locked with manual_trigger = false. TOML wins. Returns one notice
// per cleared pause for the reload result.
func (scheduler *Scheduler) PrunePauses(tasks map[string]*model.Task) []string {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	return scheduler.prunePausesLocked(context.Background(), tasks)
}

func (scheduler *Scheduler) prunePausesLocked(ctx context.Context, tasks map[string]*model.Task) []string {
	var cleared []string
	// Name order, so the reload notices read the same on every run.
	for _, name := range slices.Sorted(maps.Keys(scheduler.paused)) {
		reason := pauseClearReason(tasks[name])
		if reason == "" {
			continue
		}
		// Drop the in-memory pause even if the write fails: the config says
		// this task can't be paused, so it must not stay paused. A failed clear
		// is retried by the next boot's RestorePauses.
		if scheduler.pauses != nil {
			if err := scheduler.pauses.ResumeTaskSchedule(ctx, name, scheduler.now()); err != nil {
				slog.Error("Failed to persist cleared schedule pause", "task", name, "err", err)
			}
		}
		delete(scheduler.paused, name)
		notice := fmt.Sprintf("task %q: schedule pause cleared (%s)", name, reason)
		slog.Warn("Schedule pause cleared", "task", name, "reason", reason)
		cleared = append(cleared, notice)
	}
	return cleared
}
