// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sort"

	"log/slog"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

// RunOnStartResult summarises run_on_start firings performed at daemon boot.
type RunOnStartResult struct {
	Triggered int
	Errors    int
}

// RunStartupTasks fires every task with run_on_start set exactly once, at
// daemon boot. It is independent of cron and catch-up, and not subject to the
// catch_up cap. A run_on_start task with no cron still fires here; one with a
// cron fires here in addition to its schedule.
//
// A run_on_start = "boot" task (the cron @reboot rule) fires only when bootID
// differs from the boot it last fired in, so a daemon restart, self-update, or
// takeover within the same boot does not start it twice. The boot is recorded
// after the trigger succeeds: a crash in between re-fires it on the next start,
// which beats a boot task that silently never ran. An empty bootID (no boot
// identity on this platform) falls back to firing on every daemon start.
//
// Services are skipped — they already start every instance at boot. Held tasks
// are skipped too, and this is the one place that has to check Held rather than
// Schedulable: a crontab's `@reboot` line imports as run_on_start with no cron at
// all, so the clock-based predicate would let it through while a live cron daemon
// is firing the very same line on its own startup.
//
// Tasks are visited in name order so the firing sequence is deterministic; the
// function reads no clock, filesystem, or randomness — bootID is injected.
func RunStartupTasks(ctx context.Context, tasks map[string]*model.Task, runner TaskRunner, db storage.RunRepository, bootID string) RunOnStartResult {
	var result RunOnStartResult
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)

	if bootID == "" {
		warnNoBootID(tasks)
	}
	for _, name := range names {
		task := tasks[name]
		if !task.RunOnStart || task.Kind.IsService() || task.Held() {
			continue
		}
		perBoot := task.RunOnStartMode == model.RunOnStartBoot && bootID != ""
		if perBoot && alreadyRanThisBoot(ctx, db, name, bootID) {
			slog.Info(`run_on_start = "boot" task already ran this boot; not firing it again`, "task", name)
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
		if perBoot {
			recordBoot(ctx, db, name, bootID)
		}
	}
	return result
}

// warnNoBootID says once that "boot" tasks lost their once-per-boot guard.
func warnNoBootID(tasks map[string]*model.Task) {
	for _, task := range tasks {
		if task.RunOnStartMode == model.RunOnStartBoot {
			slog.Warn(`No boot identity on this platform; run_on_start = "boot" tasks fire on every daemon start`)
			return
		}
	}
}

func recordBoot(ctx context.Context, db storage.RunRepository, name, bootID string) {
	if err := db.SetTaskBootID(ctx, name, bootID); err != nil {
		slog.Error("Failed to record boot for run_on_start task; a daemon restart this boot will fire it again", "task", name, "err", err)
	}
}

// alreadyRanThisBoot reports whether name last fired in bootID. A read error
// counts as "no": firing twice is visible in the run history, never firing is
// not.
func alreadyRanThisBoot(ctx context.Context, db storage.RunRepository, name, bootID string) bool {
	last, err := db.GetTaskBootID(ctx, name)
	if err != nil {
		slog.Error("Failed to read last boot for run_on_start task; firing it", "task", name, "err", err)
		return false
	}
	return last == bootID
}
