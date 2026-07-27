// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeValidateConfig(t *testing.T, content string) Flags {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return Flags{CfgFile: path}
}

func TestRunValidatePrintsWarnings(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, `
[daemon]
shutdown_timeout = "5s"

[tasks.slowpoke]
run = "echo hi"
graceful_stop = "30s"
`)
	var out strings.Builder
	require.NoError(t, runValidate(&out, f, false))
	assert.Contains(t, out.String(), "is valid")
	assert.Contains(t, out.String(), "! ")
	assert.Contains(t, out.String(), "graceful_stop")
}

func TestRunValidateNoWarnings(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "[tasks.t]\nrun = \"echo hi\"\n")
	var out strings.Builder
	require.NoError(t, runValidate(&out, f, false))
	assert.Contains(t, out.String(), "is valid")
	assert.NotContains(t, out.String(), "! ")
}

func TestRunValidateRejectsBadCron(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "[tasks.t]\nrun = \"echo hi\"\ncron = \"61 * * * *\"\n")
	var out strings.Builder
	err := runValidate(&out, f, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cron")
	assert.Contains(t, err.Error(), "expected 5 fields")
}

func TestRunValidate_ValidEmpty(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "# empty\n")
	var buf bytes.Buffer
	require.NoError(t, runValidate(&buf, f, false))
	out := buf.String()
	assert.Contains(t, out, "is valid")
	assert.Contains(t, out, "tasks:    0")
	assert.Contains(t, out, "services: 0")
	assert.Contains(t, out, "timezone:")
}

func TestRunValidate_CountsTasksAndServices(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, `
[tasks.backup]
cron = "0 3 * * *"
run = "true"

[tasks.cleanup]
cron = "*/15 * * * *"
run = "true"

[services.web]
run = "exec /usr/bin/web"
`)
	var buf bytes.Buffer
	require.NoError(t, runValidate(&buf, f, false))
	out := buf.String()
	assert.Contains(t, out, "tasks:    2")
	assert.Contains(t, out, "services: 1")
}

func TestRunValidate_InvalidTOMLReturnsUserFacingError(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "this is not = valid toml ===\n")
	var buf bytes.Buffer

	err := runValidate(&buf, f, false)
	require.Error(t, err)

	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe), "expected *userFacingError, got %T", err)
	assert.Contains(t, ufe.title, "is not valid")
	assert.NotEmpty(t, ufe.details)
	assert.Empty(t, buf.String(), "no success summary on error")
}

func TestRunValidate_MissingFileReturnsUserFacingError(t *testing.T) {
	t.Parallel()
	f := Flags{CfgFile: filepath.Join(t.TempDir(), "absent.toml")}

	var buf bytes.Buffer
	err := runValidate(&buf, f, false)
	require.Error(t, err)

	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "is not valid")
}

// Regression (Bug F): validate --json must report every configuration problem,
// not just the first. A config with two unknown keys must yield two errors[]
// entries, each with its own source location — before the fix the strict-decode
// error collapsed to the first key alone.
func TestRunValidate_JSONReportsEveryUnknownKey(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, `
[tasks.t]
run = "echo hi"
boguskey = 1
anotherbogus = 2
`)
	var buf bytes.Buffer
	err := runValidate(&buf, f, true)
	require.Error(t, err)

	var doc validateJSONDoc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), "stdout must be valid JSON on failure")
	assert.False(t, doc.Valid)
	require.GreaterOrEqual(t, len(doc.Errors), 2, "each unknown key must get its own errors[] entry")
	assert.NotEqual(t, doc.Errors[0].Line, doc.Errors[1].Line, "each error must carry its own source location")
}
