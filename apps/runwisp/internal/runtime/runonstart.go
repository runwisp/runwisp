// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"maps"
	"slices"

	"log/slog"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

// RunOnStartResult summarises run_on_start firings performed at daemon boot.
type RunOnStartResult struct {
	Triggered int
	Errors    int
}

// RunStartupTasks fires every run_on_start task once at daemon boot,
// independent of cron, catch-up, and the catch_up cap.
//
// A "boot" task (cron's @reboot) fires only when bootID differs from the boot it
// last fired in, so a restart within the same boot does not repeat it. An empty
// bootID falls back to firing on every daemon start. The boot is recorded after
// the trigger succeeds: a crash in between re-fires it, which beats a boot task
// that silently never ran.
//
// Skipped: services (they start at boot anyway), paused tasks, and held tasks.
// This checks Held rather than Schedulable because an imported @reboot line has
// no cron, so the clock-based predicate would let it through while a live cron
// daemon fires the same line itself.
//
// Tasks are visited in name order so the firing sequence is deterministic; the
// function reads no clock, filesystem, or randomness (bootID is injected).
func RunStartupTasks(ctx context.Context, tasks map[string]*model.Task, runner TaskRunner, db storage.RunRepository, bootID string, paused func(string) bool) RunOnStartResult {
	var result RunOnStartResult
	if bootID == "" {
		warnNoBootID(tasks)
	}
	for _, name := range slices.Sorted(maps.Keys(tasks)) {
		task := tasks[name]
		if !task.RunOnStart || task.Kind.IsService() || task.Held() {
			continue
		}
		perBoot := task.RunOnStartMode == model.RunOnStartBoot && bootID != ""
		if skipStartupRun(ctx, db, name, bootID, perBoot, paused) {
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

// skipStartupRun reports that an otherwise eligible run_on_start task must not
// fire at this start: its schedule is paused, or it is a "boot" task that
// already ran this boot.
func skipStartupRun(ctx context.Context, db storage.RunRepository, name, bootID string, perBoot bool, paused func(string) bool) bool {
	if paused != nil && paused(name) {
		slog.Info("Skipped run_on_start: schedule paused", "task", name)
		return true
	}
	if perBoot && alreadyRanThisBoot(ctx, db, name, bootID) {
		slog.Info(`run_on_start = "boot" task already ran this boot; not firing it again`, "task", name)
		return true
	}
	return false
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
