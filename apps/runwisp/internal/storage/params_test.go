// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_ParamsRoundTrip(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "backup",
		Status:      model.PhasePending,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
		Params:      map[string]string{"source": "/data", "--force": "true"},
	}
	require.NoError(t, db.CreateRun(ctx, run))

	fetched, err := db.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"source": "/data", "--force": "true"}, fetched.Params)
}

func TestRun_NoParamsStaysNil(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "plain",
		Status:      model.PhasePending,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(ctx, run))

	fetched, err := db.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Nil(t, fetched.Params)
}
