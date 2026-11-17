// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setSeedOnly flips the package-global demoFlags into --seed-only mode for the
// duration of a test and restores them afterwards. demoFlags is a global, so
// these tests must not run in parallel with each other.
func setSeedOnly(t *testing.T, station bool) {
	t.Helper()
	prev := demoFlags
	demoFlags.SeedOnly = true
	demoFlags.Station = station
	t.Cleanup(func() { demoFlags = prev })
}

func TestRunDemoSeedOnlyWritesConfigAndSeeds(t *testing.T) {
	setSeedOnly(t, false)
	testutil.StubSleep(t) // seeding runs every demo script; their pacing is for show

	dir := t.TempDir()
	f := Flags{
		CfgFile: filepath.Join(dir, "runwisp.toml"),
		DataDir: filepath.Join(dir, "data"),
	}

	require.NoError(t, runDemo(demoCmd, f))

	// --seed-only writes the embedded demo config and seeds a real database +
	// log dir into the caller-supplied paths, then returns without spawning a
	// daemon or TUI.
	assert.FileExists(t, f.CfgFile)
	assert.FileExists(t, f.DBPath())
	assert.DirExists(t, f.LogDir())
}

func TestDemoPathFlagsRejection(t *testing.T) {
	t.Parallel()
	assert.NoError(t, demoPathFlagsRejection(false, false), "no explicit paths → demo proceeds")

	for _, tc := range []struct {
		name              string
		config, dataFlags bool
	}{
		{"config only", true, false},
		{"data only", false, true},
		{"both", true, true},
	} {
		err := demoPathFlagsRejection(tc.config, tc.dataFlags)
		require.Errorf(t, err, "%s must be rejected", tc.name)
		assert.Contains(t, err.Error(), "--seed-only")
	}
}

func TestRunDemoSeedOnlyRejectsStation(t *testing.T) {
	setSeedOnly(t, true)

	f := Flags{
		CfgFile: filepath.Join(t.TempDir(), "runwisp.toml"),
		DataDir: filepath.Join(t.TempDir(), "data"),
	}

	err := runDemo(demoCmd, f)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--seed-only cannot be combined with --station")
}

func TestDemoTerminalRejection(t *testing.T) {
	assert.NoError(t, demoTerminalRejection(false, true), "a terminal runs the TUI")
	assert.NoError(t, demoTerminalRejection(true, false), "--no-tui needs no terminal")
	err := demoTerminalRejection(false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--no-tui")
}
