// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"testing"

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
