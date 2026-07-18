// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunValidate_JSONInvalidEmitsErrorDoc(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "[tasks.t]\nrun = \"echo hi\"\ncron = \"61 * * * *\"\n")

	var buf bytes.Buffer
	err := runValidate(&buf, f, true)
	require.Error(t, err, "invalid config must still exit non-zero")

	var doc validateJSONDoc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), "stdout must be valid JSON even on failure")
	assert.Equal(t, jsonSchemaVersion, doc.SchemaVersion)
	assert.False(t, doc.Valid)
	require.Len(t, doc.Errors, 1)
	assert.Contains(t, doc.Errors[0].Message, "invalid cron")
	assert.Empty(t, doc.Warnings, "warnings is an empty array, not null")
}

func TestNewExecJSONDoc_MapsFailedAndDuration(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	end := start.Add(1500 * time.Millisecond)
	failed := model.ReasonFailed

	doc := newExecJSONDoc("backup", &model.Run{
		ID: "01JZZBACKUP0000000000000000", Status: model.PhaseEnded,
		EndReason: &failed, ExitCode: 2, TriggeredBy: model.TriggeredByAPI,
		StartAt: &start, EndAt: &end,
	})

	assert.Equal(t, jsonSchemaVersion, doc.SchemaVersion)
	assert.Equal(t, "backup", doc.Task)
	assert.Equal(t, "01JZZBACKUP0000000000000000", doc.RunID)
	require.NotNil(t, doc.ExitCode)
	assert.Equal(t, 2, *doc.ExitCode)
	require.NotNil(t, doc.EndReason)
	assert.Equal(t, "failed", *doc.EndReason)
	assert.True(t, doc.Failed, "a failure end-reason must precompute failed=true")
	require.NotNil(t, doc.DurationMS)
	assert.Equal(t, int64(1500), *doc.DurationMS)
}

func TestNewExecErrorJSONDoc_CarriesErrorAndOmitsRunFields(t *testing.T) {
	t.Parallel()
	doc := newExecErrorJSONDoc("missing", errors.New(`task "missing" not found`))

	assert.Equal(t, jsonSchemaVersion, doc.SchemaVersion)
	assert.Equal(t, "missing", doc.Task)
	assert.Equal(t, `task "missing" not found`, doc.Error)

	// The identity fields are omitted on an error doc so stdout stays clean.
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	s := string(b)
	assert.NotContains(t, s, `"run_id"`)
	assert.NotContains(t, s, `"status"`)
	assert.NotContains(t, s, `"triggered_by"`)
}

func TestRunStatus_JSONUnreachableEmitsUnhealthyDoc(t *testing.T) {
	t.Parallel()
	// No socket bound — HealthCheck fails to dial.
	f := Flags{DataDir: testutil.ShortTempDir(t)}

	var buf bytes.Buffer
	err := runStatus(&buf, f, true)
	require.Error(t, err, "an unreachable daemon must still exit non-zero")

	var doc statusJSONDoc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), "stdout must be valid JSON even when unreachable")
	assert.Equal(t, jsonSchemaVersion, doc.SchemaVersion)
	assert.False(t, doc.Healthy)
	assert.NotEmpty(t, doc.Error)
}
