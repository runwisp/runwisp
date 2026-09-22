// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
)

// stationRuntime is exactly the runtime surface the station adapter drives: the
// observe/trigger TaskRunner plus RemoveTask (service:remove tears down a
// station-declared service). A concrete runtime.TaskManager satisfies it; naming
// the seam here keeps the adapter — and its tests — from depending on the full
// TaskManager lifecycle interface.
type stationRuntime interface {
	runtime.TaskRunner
	RemoveTask(taskName string)
}

// stationTaskRunner adapts the concrete runtime task manager to station.TaskRunner.
// It embeds the runtime surface so every observe/trigger method forwards for
// free; only TriggerStationRun needs translating into the runtime's richer
// TriggerRunWithOptions API.
type stationTaskRunner struct {
	stationRuntime
}

func (a *stationTaskRunner) TriggerStationRun(taskName, executionID string, params map[string]string) (*model.Run, error) {
	return a.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy: model.TriggeredByStation,
		ExecutionID: executionID,
		// The station protocol carries plain string values (no explicit-omit state),
		// so every supplied key is a present value; absent keys use the default.
		Params: model.PointerValues(params),
	})
}
