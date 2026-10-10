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

func TestStationCmd_StationIsPrimary(t *testing.T) {
	assert.Equal(t, "station", stationCmd.Use)
	assert.Empty(t, stationCmd.Aliases)
}

func TestLoadStationEnvFile_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	require.NoError(t, os.WriteFile(path, []byte("RUNWISP_STATION_ENVFILE_TEST_KEY=loaded\n"), 0o600))

	// Pre-set env vars are not overridden; ensure clean slate.
	require.NoError(t, os.Unsetenv("RUNWISP_STATION_ENVFILE_TEST_KEY"))
	t.Cleanup(func() { _ = os.Unsetenv("RUNWISP_STATION_ENVFILE_TEST_KEY") })

	require.NoError(t, loadEnvFileInto(path, true))
	assert.Equal(t, "loaded", os.Getenv("RUNWISP_STATION_ENVFILE_TEST_KEY"))
}

func TestLoadStationEnvFile_DefaultMissingSilent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Default ".env" not explicitly requested; a missing file is silently OK.
	assert.NoError(t, loadEnvFileInto(".env", false))
}

func TestLoadStationEnvFile_ExplicitMissingErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent")

	err := loadEnvFileInto(missing, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot load env file")
}

func TestLoadStationEnvFile_ExplicitPresentValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "explicit.env")
	require.NoError(t, os.WriteFile(path, []byte("RUNWISP_EXPLICIT_TEST=ok\n"), 0o600))

	require.NoError(t, os.Unsetenv("RUNWISP_EXPLICIT_TEST"))
	t.Cleanup(func() { _ = os.Unsetenv("RUNWISP_EXPLICIT_TEST") })

	require.NoError(t, loadEnvFileInto(path, true))
	assert.Equal(t, "ok", os.Getenv("RUNWISP_EXPLICIT_TEST"))
}

// TestLoadStationEnvFile_ToleratesProjectDotenv: the default .env usually
// belongs to the surrounding project. Lines the station doesn't need, in any
// syntax, must not stop it from starting.
func TestLoadStationEnvFile_ToleratesProjectDotenv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# project settings\n" +
		"APP_ENV: production\n" +
		"TLS_KEY=\"-----BEGIN KEY-----\n" +
		"MIIB...\n" +
		"-----END KEY-----\"\n" +
		"database.url=postgres://x\n" +
		"export RUNWISP_DOTENV_TEST=tok\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	t.Setenv("RUNWISP_DOTENV_TEST", "")
	require.NoError(t, os.Unsetenv("RUNWISP_DOTENV_TEST"))

	require.NoError(t, loadEnvFileInto(path, false))
	assert.Equal(t, "tok", os.Getenv("RUNWISP_DOTENV_TEST"))
	_, set := os.LookupEnv("APP_ENV")
	assert.False(t, set, "only RUNWISP_* keys are loaded")
}

func TestParseRunwispDotenv_CommonForms(t *testing.T) {
	got := parseRunwispDotenv(`RUNWISP_A="tok" # prod
RUNWISP_B=https://station.example/x # comment
RUNWISP_C='single # not a comment'
export RUNWISP_D = spaced
RUNWISP_E="line\nbreak \"quoted\""
RUNWISP_F=url#fragment
RUNWISP_G=
# RUNWISP_H=commented
OTHER=ignored
`)
	assert.Equal(t, map[string]string{
		"RUNWISP_A": "tok",
		"RUNWISP_B": "https://station.example/x",
		"RUNWISP_C": "single # not a comment",
		"RUNWISP_D": "spaced",
		"RUNWISP_E": "line\nbreak \"quoted\"",
		"RUNWISP_F": "url#fragment",
		"RUNWISP_G": "",
	}, got)
}

func TestLoadStationEnvFile_DoesNotOverrideSetVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	require.NoError(t, os.WriteFile(path, []byte("RUNWISP_PRESET_TEST=from-file\n"), 0o600))
	t.Setenv("RUNWISP_PRESET_TEST", "from-env")

	require.NoError(t, loadEnvFileInto(path, true))
	assert.Equal(t, "from-env", os.Getenv("RUNWISP_PRESET_TEST"))
}
