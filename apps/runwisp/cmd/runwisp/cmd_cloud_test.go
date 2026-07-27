// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCloudEnvFile_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	require.NoError(t, os.WriteFile(path, []byte("RUNWISP_CLOUD_ENVFILE_TEST_KEY=loaded\n"), 0o600))

	// godotenv.Load does not override pre-set env vars; ensure clean slate.
	require.NoError(t, os.Unsetenv("RUNWISP_CLOUD_ENVFILE_TEST_KEY"))
	t.Cleanup(func() { _ = os.Unsetenv("RUNWISP_CLOUD_ENVFILE_TEST_KEY") })

	require.NoError(t, loadEnvFileInto(path, true))
	assert.Equal(t, "loaded", os.Getenv("RUNWISP_CLOUD_ENVFILE_TEST_KEY"))
}

func TestLoadCloudEnvFile_DefaultMissingSilent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Default ".env" not explicitly requested; a missing file is silently OK.
	assert.NoError(t, loadEnvFileInto(".env", false))
}

func TestLoadCloudEnvFile_ExplicitMissingErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent")

	err := loadEnvFileInto(missing, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot load env file")
}

func TestLoadCloudEnvFile_ExplicitPresentValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "explicit.env")
	require.NoError(t, os.WriteFile(path, []byte("RUNWISP_EXPLICIT_TEST=ok\n"), 0o600))

	require.NoError(t, os.Unsetenv("RUNWISP_EXPLICIT_TEST"))
	t.Cleanup(func() { _ = os.Unsetenv("RUNWISP_EXPLICIT_TEST") })

	require.NoError(t, loadEnvFileInto(path, true))
	assert.Equal(t, "ok", os.Getenv("RUNWISP_EXPLICIT_TEST"))
}
