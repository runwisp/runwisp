// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/cronprobe"
)

// cronHoldPollInterval is how often the watcher re-asks whether a system cron
// daemon is live. A minute is the resolution cron itself schedules at, so a hold
// can never be stale for longer than one tick of the thing it is holding for,
// and the steady-state cost is two short-lived systemctl processes a minute.
//
// Deliberately not a TOML setting. Nothing an operator could set here would make
// their config better, and every knob on the schema is a user-visible surface to
// keep working forever.
const cronHoldPollInterval = time.Minute

// StartCronHoldWatcher re-asks the machine whether a system cron daemon is live
// and hands the answer to refresh, so a hold releases itself when cron retires
// and comes back if cron does. It returns the func that stops the loop.
//
// It exists because the hold used to be fixed until the operator ran `runwisp
// reload`. That made the safe half of the handover safe and the finishing half a
// trap: stop cron, forget the reload, and the jobs are held by RunWisp while cron
// is no longer running them — nothing fires them at all, and no run record exists
// to make that visible. Re-probing is what turns the hold from something the
// operator maintains into something that just works.
//
// It never reads runwisp.toml. Config reload stays explicit; this only refreshes
// a fact about the machine.
//
// initial is the liveness answer the loaded config already holds, so the first
// tick only reports a change if the machine has actually moved since boot — and
// there is no boot pass, which would only exec systemctl to learn what is already
// on Config.cronDaemon.
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

// tick performs one probe and applies it if the answer moved. It is the unit-test
// seam for the loop above: the flip is the behaviour worth testing, the ticker is
// not.
//
// Only Live is compared, not the prose. A daemon going from active to
// enabled-but-stopped is still live, still owns the jobs, and re-deriving the
// same holds to rewrite one warning string would churn the live task set for
// nothing.
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
