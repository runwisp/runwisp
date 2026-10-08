// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"sync"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

type recordedSkip struct {
	taskName string
	reason   model.EndReason
}

type recordedMissed struct {
	taskName    string
	scheduledAt time.Time
	reason      string
}

// jitteredCall captures one ScheduleJitteredRun invocation so the scheduler's
// jitter tests can assert the task was routed through the gate with the
// expected tick, slot deadline, and window horizon.
type jitteredCall struct {
	taskName string
	tick     time.Time
	slot     time.Time
	window   time.Duration
}

// fakeTaskRunner is a recording RunTrigger for runtime tests. It captures
// calls in arrival order and returns a configurable result, so the scheduler,
// catch-up, and run_on_start tests share one fake without booting the
// executor, event bus, or database.
type fakeTaskRunner struct {
	mu sync.Mutex

	// triggerErr, when set, makes every TriggerRun fail without recording a
	// trigger; used to exercise catch-up error accounting.
	triggerErr error

	triggers    []string
	triggerOpts []TriggerRunOptions
	skips       []recordedSkip
	jittered    []jitteredCall
	missed      []recordedMissed
}

func (r *fakeTaskRunner) TriggerRunWithOptions(name string, opts TriggerRunOptions) (*model.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.triggerErr != nil {
		return nil, r.triggerErr
	}
	r.triggers = append(r.triggers, name)
	r.triggerOpts = append(r.triggerOpts, opts)
	return &model.Run{TaskName: name}, nil
}

func (r *fakeTaskRunner) RecordSkippedFiring(name string, reason model.EndReason, _ model.TriggeredBy) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skips = append(r.skips, recordedSkip{taskName: name, reason: reason})
	return nil
}

func (r *fakeTaskRunner) ScheduleJitteredRun(name string, tick, slot time.Time, window time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jittered = append(r.jittered, jitteredCall{taskName: name, tick: tick, slot: slot, window: window})
}

// jitteredCalls returns a copy of the recorded ScheduleJitteredRun calls, with
// locking so callers don't race the recorder.
func (r *fakeTaskRunner) jitteredCalls() []jitteredCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]jitteredCall, len(r.jittered))
	copy(out, r.jittered)
	return out
}

// triggerCount reports how many runs were triggered, with locking so callers
// don't race the recorder.
func (r *fakeTaskRunner) triggerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.triggers)
}

func (r *fakeTaskRunner) RecordMissedRun(name string, scheduledAt time.Time, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.missed = append(r.missed, recordedMissed{taskName: name, scheduledAt: scheduledAt, reason: reason})
	return nil
}
