// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// A service instance the station started is still supervised locally: when it
// exits, the slot refills just like a TriggeredByService start
// (TestServiceInstanceRefillsOnExit). Station runs skip only local task retries.
func TestStationStartedServiceInstanceRefillsOnExit(t *testing.T) {
	djm, exec, eb := newTestManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 1)
	jm.UpsertTask(task)
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 10*time.Millisecond,
	)

	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByStation))
	started.waitFor(t, 1)
	started.waitFor(t, 2)
}
