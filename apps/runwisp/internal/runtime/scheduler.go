// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"fmt"
	"sync"
	"time"

	"log/slog"

	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/jitter"
)

// ScheduleResult holds the outcome of scheduling tasks.
type ScheduleResult struct {
	Scheduled int
	Warnings  []string
}

// Scheduler wraps robfig/cron to trigger tasks on a schedule. On fall-back
// DST days, when the wall clock revisits a minute, the second firing is
// suppressed and recorded with end_reason = "dst_skipped" — there's no warning
// at boot.
type Scheduler struct {
	cron        *cron.Cron
	location    *time.Location
	taskManager TaskRunner
	tasks       map[string]*model.Task
	entryIDs    map[string]cron.EntryID
	// lastFired stores the wall-clock second (in the task's effective TZ) of
	// the last firing for each task. A second firing whose tuple matches is
	// the DST duplicate to suppress.
	lastFired map[string]wallSecond
	// jitterPlans holds, for each jittered task, its resolved slot offset, its
	// window length (the gate's free-check horizon), and the parsed schedule
	// used to clamp both to the live gap before the next tick. Computed once in
	// Start (reload is restart-only) so a task lands at the same place every
	// day. Tasks without a jitter window are absent and fire immediately.
	jitterPlans map[string]jitterPlan
	now         func() time.Time
	mutex       sync.Mutex
	started     bool
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

// SchedulerOption tweaks a Scheduler at construction time. Used today only
// to inject a fake clock from tests; kept exported so test packages outside
// `runtime` (e.g. runtime_test) can use it too.
type SchedulerOption func(*Scheduler)

// WithNow overrides the scheduler's clock. Production code uses time.Now;
// tests inject a fake clock so DST-fall-back firing sequences are
// deterministic.
func WithNow(now func() time.Time) SchedulerOption {
	return func(s *Scheduler) {
		if now != nil {
			s.now = now
		}
	}
}

// NewScheduler creates a scheduler. location controls how task cron expressions
// are interpreted; nil means UTC, which is the project default. Per-task
// timezones are layered on via a CRON_TZ= prefix when scheduling.
func NewScheduler(taskManager TaskRunner, tasks map[string]*model.Task, location *time.Location, opts ...SchedulerOption) *Scheduler {
	if location == nil {
		location = time.UTC
	}
	s := &Scheduler{
		cron:        cron.New(cron.WithLocation(location), cron.WithParser(cronspec.NewParser())),
		location:    location,
		taskManager: taskManager,
		tasks:       tasks,
		entryIDs:    make(map[string]cron.EntryID),
		lastFired:   make(map[string]wallSecond),
		jitterPlans: make(map[string]jitterPlan),
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (scheduler *Scheduler) Start() (ScheduleResult, error) {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()

	if scheduler.started {
		return ScheduleResult{}, nil
	}

	result := ScheduleResult{}
	for _, task := range scheduler.tasks {
		if task.Cron == "" {
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
// length. Called once under scheduler.mutex before the cron loop starts; the
// plans are then fixed for the daemon's lifetime (reload is restart-only).
// Determinism holds: the dial positions come from scheduler.now() and the task
// set, both injected, so the same TOML + clock yield the same slots — never
// rand, never an inline wall-clock read.
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
	now := scheduler.now()
	var windows []jitter.Window
	schedules := make(map[string]cron.Schedule)
	lengths := make(map[string]time.Duration)

	for _, task := range scheduler.tasks {
		if task.Jitter <= 0 || task.Cron == "" {
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
		length := min(task.Jitter, gap-time.Second)
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
// reconciler when a reload adds a cron task. Tasks without a cron expression
// are ignored (services have no schedule). robfig/cron's AddFunc is safe to
// call on a running cron.
func (scheduler *Scheduler) AddTask(task *model.Task) error {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()
	if task.Cron == "" {
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
	delete(scheduler.lastFired, name)
	delete(scheduler.jitterPlans, name)
}

// Reschedule replaces a task's cron entry with one built from its current
// definition, used by the reconciler when a reload changes a task's cron
// expression or timezone.
func (scheduler *Scheduler) Reschedule(task *model.Task) error {
	scheduler.RemoveTask(task.Name)
	return scheduler.AddTask(task)
}

// effectiveSpec builds the cron spec actually fed to the parser — task.Cron
// prefixed with CRON_TZ= when the task pins its own timezone — and the location
// used as the reference for wall-clock dedup (the task TZ, else the scheduler
// default). Shared by addTask and the jitter-plan computation so both interpret
// a task's schedule identically.
func (scheduler *Scheduler) effectiveSpec(task *model.Task) (string, *time.Location) {
	spec := task.Cron
	loc := scheduler.location
	if task.Timezone != "" {
		// CRON_TZ= prefix is honored by robfig/cron's standard parser and
		// overrides the scheduler's default location for this entry.
		spec = "CRON_TZ=" + task.Timezone + " " + task.Cron
		// Best-effort resolve: if it doesn't parse the AddFunc caller will
		// fail and we surface a warning. On success, use the task TZ as the
		// reference for wall-clock dedup.
		if l, err := time.LoadLocation(task.Timezone); err == nil {
			loc = l
		}
	}
	return spec, loc
}

func (scheduler *Scheduler) addTask(task *model.Task) error {
	taskName := task.Name
	spec, loc := scheduler.effectiveSpec(task)
	entryID, err := scheduler.cron.AddFunc(spec, func() {
		scheduler.fireOnce(taskName, loc)
	})
	if err == nil {
		scheduler.entryIDs[taskName] = entryID
	}
	return err
}

// fireOnce is the cron callback for a scheduled task. It dedupes wall-clock
// duplicates (DST fall-back) by tracking the task's last firing minute in
// the task's effective TZ; a duplicate is recorded as ReasonDSTSkipped and
// never reaches the executor.
func (scheduler *Scheduler) fireOnce(taskName string, loc *time.Location) {
	now := scheduler.now()
	nowLocal := now.In(loc)
	wm := newWallSecond(nowLocal)

	scheduler.mutex.Lock()
	last, hadLast := scheduler.lastFired[taskName]
	duplicate := hadLast && last == wm
	if !duplicate {
		scheduler.lastFired[taskName] = wm
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
	// stored offset and window to just under the live gap — computed from now,
	// since the cron loop has already advanced entry.Next — so a misconfigured
	// window can never push a slot onto or past the next tick. The fire is
	// submitted to the gate, which starts it at min(when it frees, the slot).
	if hasJitter {
		gapLive := plan.schedule.Next(now).Sub(now)
		limit := gapLive - time.Second
		if limit < 0 {
			limit = 0
		}
		offset := min(plan.offset, limit)
		window := min(plan.window, limit)
		slot := now.Add(offset)
		slog.Debug("Cron submitting jittered task to gate", "name", taskName, "slot_offset", offset)
		scheduler.taskManager.ScheduleJitteredRun(taskName, now, slot, window)
		return
	}

	slog.Debug("Cron triggering task", "name", taskName)
	if _, err := scheduler.taskManager.TriggerRun(taskName, model.TriggeredByCron); err != nil {
		slog.Error("Failed to trigger task", "name", taskName, "err", err)
	}
}

// GetNextRun returns the next scheduled time for the task, if scheduled.
func (scheduler *Scheduler) GetNextRun(taskName string) *string {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()

	entryID, ok := scheduler.entryIDs[taskName]
	if !ok {
		return nil
	}
	entry := scheduler.cron.Entry(entryID)
	// Surface the bare cron tick. Under the gate a jittered task starts at
	// min(when the gate frees for it, its slot): with no contention that's the
	// tick itself, and the slot is only the latest it could slip. Showing
	// tick + slot would overstate the delay, so the API/TUI/UI display the tick
	// (the earliest and most common actual start) for jittered and plain tasks
	// alike.
	formatted := entry.Next.Format("2006-01-02 15:04:05")
	return &formatted
}
