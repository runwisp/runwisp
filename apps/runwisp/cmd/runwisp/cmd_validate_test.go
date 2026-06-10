// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
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
	require.NoError(t, runValidate(&out, f))
	assert.Contains(t, out.String(), "is valid")
	assert.Contains(t, out.String(), "! ")
	assert.Contains(t, out.String(), "graceful_stop")
}

func TestRunValidateNoWarnings(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "[tasks.t]\nrun = \"echo hi\"\n")
	var out strings.Builder
	require.NoError(t, runValidate(&out, f))
	assert.Contains(t, out.String(), "is valid")
	assert.NotContains(t, out.String(), "! ")
}

func TestRunValidateRejectsBadCron(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "[tasks.t]\nrun = \"echo hi\"\ncron = \"61 * * * *\"\n")
	var out strings.Builder
	err := runValidate(&out, f)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cron")
	assert.Contains(t, err.Error(), "expected 5 fields")
}

func TestRunValidate_ValidEmpty(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "# empty\n")
	var buf bytes.Buffer
	require.NoError(t, runValidate(&buf, f))
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
	require.NoError(t, runValidate(&buf, f))
	out := buf.String()
	assert.Contains(t, out, "tasks:    2")
	assert.Contains(t, out, "services: 1")
}

func TestRunValidate_InvalidTOMLReturnsUserFacingError(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "this is not = valid toml ===\n")
	var buf bytes.Buffer

	err := runValidate(&buf, f)
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
	err := runValidate(&buf, f)
	require.Error(t, err)

	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "is not valid")
}
