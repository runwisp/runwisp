// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package autostart

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const unitPath = "/home/alice/.config/systemd/user/runwisp-bright-falcon.service"

// renderManagedUnit returns a minimal managed unit body with our
// markers — the tests don't care about the rest of the file.
func renderManagedUnit(t *testing.T, settingsHash, body string) []byte {
	t.Helper()
	return []byte(ManagedMarker + "\n" +
		"# runwisp-config-hash: " + settingsHash + "\n" +
		"# runwisp-binary-sha256: abc123\n" +
		body)
}

func TestClassifyExisting_NoFile(t *testing.T) {
	fs := NewFakeFS()
	desired := renderManagedUnit(t, "deadbeef", "[Unit]\nDescription=demo\n")
	plan, err := ClassifyExisting(fs, unitPath, desired, false)
	require.NoError(t, err)
	assert.Equal(t, PlanInstall, plan.Kind)
	assert.Equal(t, string(desired), plan.UnitContent)
}

func TestClassifyExisting_ManagedMatches_Noop(t *testing.T) {
	fs := NewFakeFS()
	desired := renderManagedUnit(t, "deadbeef", "[Unit]\nDescription=demo\n")
	require.NoError(t, fs.WriteFile(unitPath, desired, 0644))
	plan, err := ClassifyExisting(fs, unitPath, desired, false)
	require.NoError(t, err)
	assert.Equal(t, PlanNoop, plan.Kind)
}

func TestClassifyExisting_ManagedHashesDiffer_Noop(t *testing.T) {
	// Same settings content but stale hash markers — should still be
	// a Noop because the settings hash is computed over the stripped
	// body.
	fs := NewFakeFS()
	existing := renderManagedUnit(t, "stale-hash", "[Unit]\nDescription=demo\n")
	require.NoError(t, fs.WriteFile(unitPath, existing, 0644))
	desired := renderManagedUnit(t, "fresh-hash", "[Unit]\nDescription=demo\n")
	plan, err := ClassifyExisting(fs, unitPath, desired, false)
	require.NoError(t, err)
	assert.Equal(t, PlanNoop, plan.Kind,
		"hash drift in the marker without settings drift must not flip to Update")
}

func TestClassifyExisting_ManagedDrift_Update(t *testing.T) {
	fs := NewFakeFS()
	existing := renderManagedUnit(t, "deadbeef", "[Unit]\nDescription=old\n")
	require.NoError(t, fs.WriteFile(unitPath, existing, 0644))
	desired := renderManagedUnit(t, "deadbeef", "[Unit]\nDescription=new\n")
	plan, err := ClassifyExisting(fs, unitPath, desired, false)
	require.NoError(t, err)
	assert.Equal(t, PlanUpdate, plan.Kind)
	assert.Contains(t, plan.Diff, "-Description=old")
	assert.Contains(t, plan.Diff, "+Description=new")
}

func TestClassifyExisting_HandWritten_Conflict(t *testing.T) {
	fs := NewFakeFS()
	require.NoError(t, fs.WriteFile(unitPath, []byte("[Unit]\nDescription=mine\n"), 0644))
	desired := renderManagedUnit(t, "deadbeef", "[Unit]\nDescription=demo\n")
	plan, err := ClassifyExisting(fs, unitPath, desired, false)
	require.NoError(t, err)
	assert.Equal(t, PlanConflict, plan.Kind)
}

func TestClassifyExisting_HandWritten_ForceUpdates(t *testing.T) {
	fs := NewFakeFS()
	require.NoError(t, fs.WriteFile(unitPath, []byte("[Unit]\nDescription=mine\n"), 0644))
	desired := renderManagedUnit(t, "deadbeef", "[Unit]\nDescription=demo\n")
	plan, err := ClassifyExisting(fs, unitPath, desired, true)
	require.NoError(t, err)
	assert.Equal(t, PlanUpdate, plan.Kind)
	assert.NotEmpty(t, plan.Diff, "--force update path must render a diff")
}

func TestClassifyUninstall_NoFile_Noop(t *testing.T) {
	plan, err := ClassifyUninstall(NewFakeFS(), unitPath, false)
	require.NoError(t, err)
	assert.Equal(t, PlanNoop, plan.Kind)
}

func TestClassifyUninstall_Managed_Uninstall(t *testing.T) {
	fs := NewFakeFS()
	require.NoError(t, fs.WriteFile(unitPath, renderManagedUnit(t, "x", "body\n"), 0644))
	plan, err := ClassifyUninstall(fs, unitPath, false)
	require.NoError(t, err)
	assert.Equal(t, PlanUninstall, plan.Kind)
}

func TestClassifyUninstall_HandWritten_Conflict(t *testing.T) {
	fs := NewFakeFS()
	require.NoError(t, fs.WriteFile(unitPath, []byte("hand-written\n"), 0644))
	plan, err := ClassifyUninstall(fs, unitPath, false)
	require.NoError(t, err)
	assert.Equal(t, PlanConflict, plan.Kind)
}

func TestClassifyUninstall_HandWritten_ForceUninstalls(t *testing.T) {
	fs := NewFakeFS()
	require.NoError(t, fs.WriteFile(unitPath, []byte("hand-written\n"), 0644))
	plan, err := ClassifyUninstall(fs, unitPath, true)
	require.NoError(t, err)
	assert.Equal(t, PlanUninstall, plan.Kind)
}

func TestSettingsHash_Stable(t *testing.T) {
	a := SettingsHash("/usr/bin/runwisp", "/etc/runwisp.toml", "/var/lib/runwisp", "127.0.0.1", 9477)
	b := SettingsHash("/usr/bin/runwisp", "/etc/runwisp.toml", "/var/lib/runwisp", "127.0.0.1", 9477)
	assert.Equal(t, a, b)
	// Twelve hex chars.
	assert.Len(t, a, 12)
}

func TestSettingsHash_Sensitive(t *testing.T) {
	base := SettingsHash("/usr/bin/runwisp", "/etc/runwisp.toml", "/var/lib/runwisp", "127.0.0.1", 9477)
	alt := SettingsHash("/usr/bin/runwisp", "/etc/runwisp.toml", "/var/lib/runwisp", "127.0.0.1", 9478)
	assert.NotEqual(t, base, alt, "different port must produce different hash")
}

func TestExtractMarkers(t *testing.T) {
	body := renderManagedUnit(t, "deadbeef", "[Unit]\nDescription=demo\n")
	parsed := extractMarkers(body)
	assert.True(t, parsed.managed)
	assert.Equal(t, "deadbeef", parsed.configHash)
	assert.Equal(t, "abc123", parsed.binarySHA)
}

func TestExtractMarkers_PlistComments(t *testing.T) {
	body := []byte(strings.Join([]string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<!-- " + managedMarkerBare + " -->",
		"<!-- runwisp-config-hash: feedface -->",
		"<!-- runwisp-binary-sha256: 0011223344 -->",
		"<plist><dict></dict></plist>",
	}, "\n"))
	parsed := extractMarkers(body)
	assert.True(t, parsed.managed, "the HTML-comment form of the managed marker must be recognised")
	assert.Equal(t, "feedface", parsed.configHash)
	assert.Equal(t, "0011223344", parsed.binarySHA)
}
