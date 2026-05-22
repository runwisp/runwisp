// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"fmt"
	"sync"
	"time"

	"log/slog"

	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/internal/model"
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
	// lastFired stores the wall-clock minute (in the task's effective TZ) of
	// the last firing for each task. A second firing whose tuple matches is
	// the DST duplicate to suppress.
	lastFired map[string]wallMinute
	now       func() time.Time
	mutex     sync.Mutex
	started   bool
}

// wallMinute is a (date, hour, minute) tuple that lets us compare two
// firings as "the same wall-clock minute" without depending on UTC offsets,
// which is the whole point on DST fall-back days.
type wallMinute struct {
	year   int
	month  time.Month
	day    int
	hour   int
	minute int
}

func newWallMinute(t time.Time) wallMinute {
	return wallMinute{
		year:   t.Year(),
		month:  t.Month(),
		day:    t.Day(),
		hour:   t.Hour(),
		minute: t.Minute(),
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
		cron:        cron.New(cron.WithLocation(location)),
		location:    location,
		taskManager: taskManager,
		tasks:       tasks,
		entryIDs:    make(map[string]cron.EntryID),
		lastFired:   make(map[string]wallMinute),
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

	scheduler.cron.Start()
	scheduler.started = true
	return result, nil
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

func (scheduler *Scheduler) addTask(task *model.Task) error {
	taskName := task.Name
	spec := task.Cron
	loc := scheduler.location
	if task.Timezone != "" {
		// CRON_TZ= prefix is honored by robfig/cron's standard parser and
		// overrides the scheduler's default location for this entry.
		spec = "CRON_TZ=" + task.Timezone + " " + task.Cron
		// Best-effort resolve: if it doesn't parse the AddFunc below will
		// fail and we surface a warning. On success, use the task TZ as the
		// reference for wall-clock dedup.
		if l, err := time.LoadLocation(task.Timezone); err == nil {
			loc = l
		}
	}
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
	nowLocal := scheduler.now().In(loc)
	wm := newWallMinute(nowLocal)

	scheduler.mutex.Lock()
	last, hadLast := scheduler.lastFired[taskName]
	duplicate := hadLast && last == wm
	if !duplicate {
		scheduler.lastFired[taskName] = wm
	}
	scheduler.mutex.Unlock()

	if duplicate {
		slog.Info("Suppressed duplicate cron firing on DST fall-back",
			"task", taskName,
			"wall_clock", nowLocal.Format("2006-01-02 15:04 MST"),
		)
		if err := scheduler.taskManager.RecordSkippedFiring(taskName, model.ReasonDSTSkipped, model.TriggeredByCron); err != nil {
			slog.Error("Failed to record DST-skipped firing", "task", taskName, "err", err)
		}
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
	next := entry.Next.Format("2006-01-02 15:04:05")
	return &next
}
