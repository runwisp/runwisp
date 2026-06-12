// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

type recordedSkip struct {
	taskName string
	reason   model.EndReason
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

// fakeTaskRunner is a recording TaskRunner stand-in for runtime tests. It
// captures trigger/skip calls in arrival order and returns a configurable
// result, so both the scheduler's golden firing-sequence tests and the
// catch-up tests share one fake without booting the executor, event bus, or
// database.
//
// It lives in package runtime (not testutil) on purpose: the runtime test
// binary cannot import a testutil that imports runtime, so a shared fake for
// the TaskRunner interface has to be in-package.
type fakeTaskRunner struct {
	mu sync.Mutex

	// triggerErr, when set, makes every TriggerRun fail without recording a
	// trigger — used to exercise catch-up error accounting.
	triggerErr error

	triggers []string
	skips    []recordedSkip
	jittered []jitteredCall
}

func (r *fakeTaskRunner) TriggerRun(name string, _ model.TriggeredBy) (*model.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.triggerErr != nil {
		return nil, r.triggerErr
	}
	r.triggers = append(r.triggers, name)
	return &model.Run{TaskName: name}, nil
}

func (r *fakeTaskRunner) TriggerRunWithOptions(name string, _ TriggerRunOptions) (*model.Run, error) {
	return r.TriggerRun(name, model.TriggeredByCron)
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

func (r *fakeTaskRunner) RecordMissedRun(string, time.Time, string) error { return nil }

func (r *fakeTaskRunner) GetTask(string) (*model.Task, bool)             { return nil, false }
func (r *fakeTaskRunner) UpsertTask(*model.Task)                         {}
func (r *fakeTaskRunner) TerminateRun(string) error                      { return nil }
func (r *fakeTaskRunner) TerminateRunByExternalExecutionID(string) error { return nil }
func (r *fakeTaskRunner) RestartServiceInstances(string) error           { return nil }
func (r *fakeTaskRunner) StopService(string) error                       { return nil }
func (r *fakeTaskRunner) GetActiveRunCount(string) int                   { return 0 }
