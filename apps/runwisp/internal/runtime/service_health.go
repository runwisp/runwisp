// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/health"
)

// watchRun is the executor's RunWatcher. For a live instance of a service with
// a health_check it runs the check until the instance exits, marking the slot
// healthy on the first pass and stopping the instance as unhealthy when the
// check gives up on it. Every other run returns immediately.
//
// The executor joins this before it returns the run's result, so a
// MarkHealthy always lands before the exit reaches retireRun/RecordExit.
func (m *defaultTaskManager) watchRun(ctx context.Context, task *model.Task, run *model.Run, ctl executor.RunControl) {
	probe := task.HealthCheck
	if !task.Kind.IsService() || probe == nil || run.StartedAt == nil {
		return
	}
	m.mu.RLock()
	daemonLocation := m.daemonLocation
	m.mu.RUnlock()
	var loc *time.Location
	if daemonLocation != nil {
		// Read at watch time, not stamped into the probe by config, so a
		// [daemon] timezone reload leaves the service definition unchanged.
		loc = daemonLocation()
	}
	spec, loc := resolveTaskSchedule(probe, loc)
	schedule, err := cronspec.NewScheduleParser().Parse(spec)
	if err != nil {
		// Load validated this exact spec, so this is unreachable short of a
		// hand-built task; say so rather than silently never checking.
		slog.Error("Health check schedule does not parse; instance is not health-checked",
			"task", task.Name, "cron", probe.Cron, "err", err)
		ctl.Annotate("health check disabled: invalid cron " + probe.Cron)
		return
	}
	w := &health.Watcher{
		Probe:        probe,
		Schedule:     zonedSchedule{schedule, loc},
		Started:      *run.StartedAt,
		HealthyAfter: config.OrDefault(task.HealthyAfter, config.DefaultHealthyAfter),
		Now:          m.clock,
		Sleep:        health.Sleep,
		Check: func(ctx context.Context) executor.ProbeResult {
			return ctl.Probe(ctx, probe)
		},
		OnHealthy: func() { m.markServiceHealthy(task.Name, run) },
		Annotate:  ctl.Annotate,
	}
	if w.Watch(ctx) {
		ctl.Kill(model.ReasonUnhealthy)
	}
}

// markServiceHealthy records a passed health check on the slot run occupies.
func (m *defaultTaskManager) markServiceHealthy(taskName string, run *model.Run) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts := m.taskStateFor(taskName); ts != nil && ts.supervisor != nil {
		ts.supervisor.MarkHealthy(run.InstanceIndex)
	}
}
