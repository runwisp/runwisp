// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_ResourceUsageRoundTrip(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "backup",
		Status:      model.PhaseRunning,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(ctx, run))

	fetched, err := db.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Nil(t, fetched.PeakMemoryBytes, "unmeasured stays nil")
	assert.Nil(t, fetched.CPUTimeMs)

	peak, cpu := int64(48<<20), int64(1200)
	run.PeakMemoryBytes, run.CPUTimeMs = &peak, &cpu
	require.NoError(t, db.UpdateRun(ctx, run))

	fetched, err = db.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, &peak, fetched.PeakMemoryBytes)
	assert.Equal(t, &cpu, fetched.CPUTimeMs)

	listed, err := db.QueryRuns(ctx, RunQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, &peak, listed[0].PeakMemoryBytes, "list queries carry the columns too")
	assert.Equal(t, &cpu, listed[0].CPUTimeMs)
}
