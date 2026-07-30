// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveInstallScope_DefaultsToSystemAsRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the system scope only exists on Linux today")
	}
	systemWide, err := resolveInstallScope(false, 0)
	require.NoError(t, err)
	assert.True(t, systemWide, "the default install is the system-wide singleton")
}

// The whole point of refusing rather than quietly falling back to a
// user-scoped unit: an operator who typed `runwisp service install` on a
// VPS wants the box's scheduler, not a unit that dies with their session.
func TestResolveInstallScope_NonRootDefaultRefuses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("macOS refuses the system scope for a different reason")
	}
	_, err := resolveInstallScope(false, 1000)
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.title, "requires root")
	assert.Contains(t, ufe.details, "sudo runwisp service install")
	assert.Contains(t, ufe.details, "--local")
}

func TestResolveInstallScope_DarwinRefusesSystemScope(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific refusal")
	}
	_, err := resolveInstallScope(false, 0)
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.title, "not supported on macOS")
	assert.Contains(t, ufe.details, "--local")
}

func TestResolveInstallScope_LocalIsAlwaysUserScope(t *testing.T) {
	systemWide, err := resolveInstallScope(true, 1000)
	require.NoError(t, err)
	assert.False(t, systemWide)
}

// systemctl --user has no bus for root under sudo, so a --local unit
// installed that way would be written and then never start.
func TestResolveInstallScope_LocalAsRootRefusesOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the systemctl --user bus problem is Linux-specific")
	}
	_, err := resolveInstallScope(true, 0)
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.title, "--local does not work as root")
}

// scopeDeps builds Deps whose candidate unit paths land inside dir, with
// the named unit files pre-created.
func scopeDeps(t *testing.T, present ...string) autostart.Deps {
	t.Helper()
	fs := autostart.NewFakeFS()
	deps := autostart.Deps{
		FS:          fs,
		Home:        "/home/tester",
		User:        "tester",
		Fingerprint: "fp-test",
	}
	systemPath, userPath := autostart.ScopeCandidates(deps)
	for _, which := range present {
		path := systemPath
		if which == "user" {
			path = userPath
		}
		require.NotEmpty(t, path, "scope %q does not exist on %s", which, runtime.GOOS)
		require.NoError(t, fs.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, fs.WriteFile(path, []byte(autostart.ManagedMarker), 0o644))
	}
	return deps
}

func TestResolveManagedScope_LocalFlagPinsUserScope(t *testing.T) {
	systemWide, err := resolveManagedScope(scopeDeps(t), true)
	require.NoError(t, err)
	assert.False(t, systemWide)
}

func TestResolveManagedScope_FindsTheUserUnit(t *testing.T) {
	systemWide, err := resolveManagedScope(scopeDeps(t, "user"), false)
	require.NoError(t, err)
	assert.False(t, systemWide)
}

func TestResolveManagedScope_FindsTheSystemUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("no system scope outside Linux")
	}
	systemWide, err := resolveManagedScope(scopeDeps(t, "system"), false)
	require.NoError(t, err)
	assert.True(t, systemWide)
}

// Nothing installed still has to answer something: the system scope, since
// that is what `service install` would create.
func TestResolveManagedScope_NothingInstalledReportsSystemScope(t *testing.T) {
	systemWide, err := resolveManagedScope(scopeDeps(t), false)
	require.NoError(t, err)
	assert.Equal(t, runtime.GOOS == "linux", systemWide)
}

// Guessing here is how `service uninstall` used to report "nothing to do"
// while a live unit stayed installed — so it asks instead.
func TestResolveManagedScope_BothInstalledIsAmbiguous(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux can have both scopes installed")
	}
	_, err := resolveManagedScope(scopeDeps(t, "system", "user"), false)
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.title, "both a system-wide and a user unit")
	assert.Contains(t, ufe.details, "--local")
}
