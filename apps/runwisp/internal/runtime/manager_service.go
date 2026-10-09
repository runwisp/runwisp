// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// StartServiceInstances brings every instance of a service up to its desired
// count. Idempotent — already-running instances are left untouched. The
// triggeredBy argument labels the resulting runs: daemon boot passes
// TriggeredByService; an operator-initiated REST restart of a stopped service
// passes TriggeredByAPI.
func (m *defaultTaskManager) StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error {
	m.mu.RLock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.RUnlock()
		return err
	}
	if ts.supervisor.IsStopped() {
		m.mu.RUnlock()
		return nil
	}
	missing := ts.supervisor.MissingSlots()
	m.mu.RUnlock()

	for _, i := range missing {
		if _, err := m.TriggerRunWithOptions(taskName, TriggerRunOptions{
			TriggeredBy:   triggeredBy,
			InstanceIndex: &i,
		}); err != nil {
			slog.Error("Failed to start service instance", "task", taskName, "instance", i, "err", err)
		}
	}
	return nil
}

// serviceLocked looks up a service task, rejecting unknown names and
// non-services. Caller must hold m.mu (read or write).
func (m *defaultTaskManager) serviceLocked(taskName string) (*taskState, error) {
	ts, exists := m.tasks[taskName]
	if !exists {
		return nil, fmt.Errorf(errTaskNotFoundFmt, taskName)
	}
	if !ts.task.Kind.IsService() {
		return nil, fmt.Errorf(errTaskNotServiceFmt, taskName)
	}
	return ts, nil
}

// StartService clears a service's stop and FATAL flags, then brings it up to
// its desired instance count (StartServiceInstances alone no-ops on a stopped
// service). Already-live instances are left untouched; nothing is cancelled.
func (m *defaultTaskManager) StartService(taskName string) error {
	m.mu.Lock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	wasStopped := ts.supervisor.IsStopped()
	ts.supervisor.MarkRunning()
	ts.bookkeepingStop = false
	m.mu.Unlock()

	if wasStopped {
		m.publishTasksChanged()
	}
	return m.StartServiceInstances(taskName, model.TriggeredByAPI)
}

// RestartServiceInstances brings a service back to its desired instance count.
// If the service was operator-stopped or had FATAL instances, the stop/FATAL
// flags are cleared and the empty slots are spawned with a fresh start-retry
// budget. If the service is already running, every active instance is cancelled
// and the exit handler refills the freed slots via the supervisor.
func (m *defaultTaskManager) RestartServiceInstances(taskName string) error {
	m.mu.Lock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	// Capture FATAL state before MarkRunning clears it: a FATAL service has no
	// active runs in its dead slots, so they only come back if we spawn them.
	wasStopped := ts.supervisor.IsStopped()
	wasFatal := ts.supervisor.IsAnyFatal()
	ts.supervisor.MarkRunning()
	ts.bookkeepingStop = false
	for _, ar := range ts.active {
		ar.Cancel()
	}
	m.mu.Unlock()

	if wasStopped {
		m.publishTasksChanged()
	}
	if wasStopped || wasFatal {
		return m.StartServiceInstances(taskName, model.TriggeredByAPI)
	}
	return nil
}

// RecycleServiceInstances lets a reload-changed service definition (new
// command, env, or instance count) take effect without treating the reload
// as an operator restart: a stopped or never-autostarted service is left as
// it is, and a FATAL instance stays FATAL. When the service is running, every
// live instance is cancelled so the exit handler respawns it under the new
// definition, and any slots added by an instance-count increase are filled.
func (m *defaultTaskManager) RecycleServiceInstances(taskName string) error {
	m.mu.Lock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if ts.supervisor.IsStopped() {
		m.mu.Unlock()
		return nil
	}
	for _, ar := range ts.active {
		ar.Cancel()
	}
	m.mu.Unlock()

	return m.StartServiceInstances(taskName, model.TriggeredByService)
}

// StopService marks the service as operator-stopped (in-memory only, cleared
// on daemon restart) and cancels every live instance. The exit handler honours
// the flag and stops refilling slots.
func (m *defaultTaskManager) StopService(taskName string) error {
	m.mu.Lock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	wasStopped := ts.supervisor.IsStopped()
	ts.supervisor.MarkStopped()
	ts.bookkeepingStop = false
	for _, ar := range ts.active {
		ar.Cancel()
	}
	m.mu.Unlock()

	if !wasStopped {
		m.publishTasksChanged()
	}
	return nil
}

// publishTasksChanged tells dashboards to refetch /api/tasks after an operator
// start/stop flipped a service's stopped state (TaskResponse.ServiceStopped).
// Published here, not in the REST layer, so a station-driven stop shows up too.
// Callers must not hold m.mu: bus handlers run synchronously.
func (m *defaultTaskManager) publishTasksChanged() {
	m.eventBus.Publish(events.EventTasksChanged, events.TasksChangedEvent{})
}

// ServiceHealthy reports whether a service currently has at least one healthy
// instance (see services.Supervisor.IsHealthy). Non-services and unknown tasks
// report false. This is the live readiness signal consumed by depends_on boot
// gating.
func (m *defaultTaskManager) ServiceHealthy(taskName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ts, ok := m.tasks[taskName]
	if !ok || ts.supervisor == nil {
		return false
	}
	return ts.supervisor.IsHealthy()
}

// WaitServiceHealthy blocks until the named service is healthy, the context is
// cancelled, or the service can no longer reach healthy without operator
// intervention (operator-stopped, or every live slot gone and a FATAL one
// left). It returns nil only on healthy; every other exit is an error so the
// caller can decide whether to proceed anyway. It polls rather than waiting on
// a condition variable: uptime-based readiness (healthy_after) crosses its
// threshold with no state change to signal on.
func (m *defaultTaskManager) WaitServiceHealthy(ctx context.Context, taskName string) error {
	if m.ServiceHealthy(taskName) {
		return nil
	}
	ticker := time.NewTicker(serviceHealthPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if m.ServiceHealthy(taskName) {
				return nil
			}
			if giveUp, err := m.serviceUnrecoverable(taskName); giveUp {
				return err
			}
		}
	}
}

// serviceUnrecoverable reports whether a service has no path back to healthy
// without operator action, so WaitServiceHealthy can stop polling early instead
// of burning the whole bounded window on a service that will never come up.
func (m *defaultTaskManager) serviceUnrecoverable(taskName string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ts, ok := m.tasks[taskName]
	if !ok {
		return true, fmt.Errorf(errTaskNotFoundFmt, taskName)
	}
	if ts.supervisor == nil {
		return true, fmt.Errorf(errTaskNotServiceFmt, taskName)
	}
	if ts.supervisor.IsStopped() {
		return true, fmt.Errorf("service %s is stopped", taskName)
	}
	if ts.supervisor.IsAnyFatal() && ts.supervisor.LiveCount() == 0 {
		return true, fmt.Errorf("service %s has no live instances and a slot is FATAL", taskName)
	}
	return false, nil
}

// ServiceSnapshot returns the supervisor + live-run view of a service task for
// reporting to station. ok is false when the task is unknown or not a service.
// Built under the manager lock so it is a consistent point-in-time.
func (m *defaultTaskManager) ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ts, exists := m.tasks[taskName]
	if !exists || ts.supervisor == nil || !ts.task.Kind.IsService() {
		return model.ServiceSnapshot{}, false
	}

	// Index live runs by instance slot to enrich slots with their start time.
	liveByIdx := make(map[int]*ActiveRun, len(ts.active))
	for _, ar := range ts.active {
		if ar.Run != nil {
			liveByIdx[ar.Run.InstanceIndex] = ar
		}
	}

	stopped := ts.supervisor.IsStopped()
	desired := ts.supervisor.Instances()
	instances := make([]model.ServiceInstanceStatus, 0, desired)
	running := 0
	fatal := 0
	for i := 0; i < desired; i++ {
		st := model.ServiceInstanceStatus{
			Index:        i,
			RestartCount: ts.supervisor.Attempts(i),
			StartFails:   ts.supervisor.StartFails(i),
			LastExitCode: ts.supervisor.LastExitCode(i),
		}
		switch {
		case ts.supervisor.IsLive(i):
			st.State = model.ServiceInstanceRunning
			running++
			if ar := liveByIdx[i]; ar != nil {
				started := ar.StartedAt
				st.StartedAt = &started
			}
		case stopped:
			st.State = model.ServiceInstanceStopped
		case ts.supervisor.IsFatal(i):
			// Slot exhausted its start-retry budget; the supervisor has given
			// up on it and won't respawn without operator intervention.
			st.State = model.ServiceInstanceFatal
			fatal++
		default:
			// Not live, not operator-stopped, not fatal: between exit and the
			// next backoff-delayed respawn.
			st.State = model.ServiceInstanceRestarting
		}
		instances = append(instances, st)
	}

	return model.ServiceSnapshot{
		TaskName:         taskName,
		State:            serviceRollupState(stopped, running, fatal, desired),
		DesiredInstances: desired,
		RunningInstances: running,
		Instances:        instances,
	}, true
}

func serviceRollupState(stopped bool, running, fatal, desired int) string {
	switch {
	case stopped:
		return model.ServiceStopped
	case running >= desired:
		return model.ServiceRunning
	case fatal >= desired:
		// Every slot has given up — the service is down and won't recover on
		// its own. Distinct from degraded, which is still trying to respawn.
		return model.ServiceFatal
	default:
		return model.ServiceDegraded
	}
}
