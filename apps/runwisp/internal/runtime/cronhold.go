// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/cronprobe"
)

// cronHoldPollInterval is how often the watcher re-asks whether a system cron
// daemon is live. A minute is the resolution cron itself schedules at, so a hold
// can never be stale for longer than one tick of the thing it is holding for,
// and the steady-state cost is two short-lived systemctl processes a minute.
//
// Not a TOML setting: no value an operator could pick would improve their
// config.
const cronHoldPollInterval = time.Minute

// StartCronHoldWatcher re-asks the machine whether a system cron daemon is live
// and hands the answer to refresh, so a hold releases itself when cron retires
// and comes back if cron does. It returns the func that stops the loop.
//
// Without it, stopping cron without a `runwisp reload` would leave the jobs held
// by RunWisp and fired by nobody, with no run record to show it.
//
// It never reads runwisp.toml. Config reload stays explicit; this only refreshes
// a fact about the machine.
//
// initial is the liveness answer the loaded config already holds, so the first
// tick only reports a change if the machine has moved since boot. There is no
// boot pass: it would only re-learn what Config.cronDaemon already says.
func StartCronHoldWatcher(
	probe func() cronprobe.State,
	refresh func(cronprobe.State) CronHoldChange,
	initial cronprobe.State,
) context.CancelFunc {
	w := &cronHoldWatcher{probe: probe, refresh: refresh, last: initial}
	ctx, cancel := context.WithCancel(context.Background())
	startTicker(ctx, cronHoldPollInterval, "Stopping cron hold watcher", func(context.Context) { w.tick() })
	return cancel
}

type cronHoldWatcher struct {
	probe   func() cronprobe.State
	refresh func(cronprobe.State) CronHoldChange
	last    cronprobe.State
}

// tick performs one probe and applies it if the answer moved.
//
// Only Live is compared, not the prose: a daemon going from active to
// enabled-but-stopped still owns the jobs, and re-deriving the same holds to
// rewrite one warning string would churn the live task set for nothing.
func (w *cronHoldWatcher) tick() {
	state := w.probe()
	if state.Live == w.last.Live {
		return
	}
	w.last = state

	change := w.refresh(state)
	if n := len(change.Released); n > 0 {
		slog.Info("System cron is gone; RunWisp now owns these tasks",
			"count", n, "tasks", change.Released)
	}
	if n := len(change.Held); n > 0 {
		slog.Warn("A system cron daemon is live again; standing down so nothing fires twice",
			"count", n, "tasks", change.Held, "cron", state.State)
	}
}
