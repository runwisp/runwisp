// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// eventTypeLog records the type of every run event in arrival order.
func eventTypeLog(eb *events.Bus) *runWaiter {
	return watchRuns(eb, events.EventRunLongRunning, events.EventRunCompleted, events.EventRunFailed)
}

func TestLongRunningWarningFiresOnceBeforeTerminalEvent(t *testing.T) {
	jm, gate, eb := newGatedManager(t)
	task := testTask("slow", model.PolicySkip, 1)
	task.WarnAfter = durPtr(20 * time.Millisecond)
	jm.UpsertTask(task)

	var (
		mu    sync.Mutex
		types []events.EventType
	)
	long := make(chan struct{}, 4)
	for _, et := range []events.EventType{events.EventRunLongRunning, events.EventRunCompleted, events.EventRunFailed} {
		eb.Subscribe(et, func(e events.Event) {
			mu.Lock()
			types = append(types, e.Type)
			mu.Unlock()
			if e.Type == events.EventRunLongRunning {
				long <- struct{}{}
			}
		})
	}
	done := watchCompletions(eb)

	_, err := jm.TriggerRunWithOptions("slow", TriggerRunOptions{TriggeredBy: model.TriggeredByAPI})
	require.NoError(t, err)
	gate.WaitStarted(t)

	select {
	case <-long:
	case <-time.After(2 * time.Second):
		t.Fatal("no long_running event while the run was still in flight")
	}
	gate.ReleaseAll()
	done.waitFor(t, 1)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []events.EventType{events.EventRunLongRunning, events.EventRunCompleted}, types,
		"exactly one long_running, ahead of the terminal event")
}

func TestLongRunningWarningSilentWhenRunFinishesFirst(t *testing.T) {
	for name, warn := range map[string]*time.Duration{
		"threshold not reached": durPtr(time.Hour),
		"no warn_after":         nil,
		"explicit zero":         durPtr(0),
	} {
		t.Run(name, func(t *testing.T) {
			jm, exec, eb := newTestManager(t)
			task := testTask("quick", model.PolicySkip, 1)
			task.WarnAfter = warn
			jm.UpsertTask(task)
			exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

			log := eventTypeLog(eb)
			_, err := jm.TriggerRunWithOptions("quick", TriggerRunOptions{TriggeredBy: model.TriggeredByAPI})
			require.NoError(t, err)
			log.waitFor(t, 1)

			// The timer is disarmed before the terminal event, so by now any
			// stray long_running would already be recorded.
			assert.Equal(t, 1, log.count(), "only the terminal event, no long_running")
		})
	}
}
