// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeValidateConfig(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	prev := flags.CfgFile
	flags.CfgFile = path
	t.Cleanup(func() { flags.CfgFile = prev })
}

func TestRunValidatePrintsWarnings(t *testing.T) {
	writeValidateConfig(t, `
[daemon]
shutdown_timeout = "5s"

[tasks.slowpoke]
run = "echo hi"
graceful_stop = "30s"
`)
	var out strings.Builder
	require.NoError(t, runValidate(&out))
	assert.Contains(t, out.String(), "is valid")
	assert.Contains(t, out.String(), "! ")
	assert.Contains(t, out.String(), "graceful_stop")
}

func TestRunValidateNoWarnings(t *testing.T) {
	writeValidateConfig(t, "[tasks.t]\nrun = \"echo hi\"\n")
	var out strings.Builder
	require.NoError(t, runValidate(&out))
	assert.Contains(t, out.String(), "is valid")
	assert.NotContains(t, out.String(), "! ")
}

func TestRunValidateRejectsBadCron(t *testing.T) {
	writeValidateConfig(t, "[tasks.t]\nrun = \"echo hi\"\ncron = \"61 * * * *\"\n")
	var out strings.Builder
	err := runValidate(&out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cron")
	assert.Contains(t, err.Error(), "expected 5 fields")
}
