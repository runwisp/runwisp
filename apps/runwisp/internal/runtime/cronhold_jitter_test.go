// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/cronprobe"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Releasing a held task re-levels the jitter dial the way a reload does, so the
// task fires through the gate instead of at the raw tick until the next reload.
func TestRefreshCronHolds_ReleasedTaskGetsJitterPlan(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 2, 0, 0, time.UTC)
	jitterWindow := 5 * time.Minute
	held := &model.Task{
		Name:       "backup",
		Cron:       "0 3 * * *",
		Run:        "backup.sh",
		Source:     model.SourceCron,
		SourceFile: "/etc/cron.d/backup",
		HeldBy:     model.HeldByCron,
		Jitter:     &jitterWindow,
	}

	r, sched, _ := holdRefreshFixture(t, now, held)
	require.Nil(t, sched.GetNextRun("backup"), "held to begin with, or this proves nothing")

	change := r.RefreshCronHolds(cronprobe.State{}) // system cron is gone
	require.Equal(t, []string{"backup"}, change.Released)
	require.NotNil(t, sched.GetNextRun("backup"), "cron is gone, so RunWisp must be firing this now")

	sched.mutex.Lock()
	_, hasPlan := sched.jitterPlans["backup"]
	sched.mutex.Unlock()

	assert.True(t, hasPlan,
		"a released task with a configured jitter window must get a jitter plan "+
			"so it fires through the work-conserving gate instead of at the raw tick")
}
