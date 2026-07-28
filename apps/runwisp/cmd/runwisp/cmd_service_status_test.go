// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveStatusOptions_PullsFromFlags(t *testing.T) {
	t.Parallel()
	f := Flags{
		CfgFile: "/tmp/status-test.toml",
		DataDir: "/tmp/status-data",
		Host:    "10.0.0.1",
		Port:    9911,
	}

	opts, err := resolveStatusOptions(f, false)
	require.NoError(t, err)
	assert.NotEmpty(t, opts.Binary, "binary path must come from os.Executable")
	assert.Equal(t, "/tmp/status-test.toml", opts.Config)
	assert.Equal(t, "/tmp/status-data", opts.DataDir)
	assert.Equal(t, "10.0.0.1", opts.Host)
	assert.Equal(t, 9911, opts.Port)
	assert.False(t, opts.System)
}

func TestResolveStatusOptions_SystemWide(t *testing.T) {
	t.Parallel()
	opts, err := resolveStatusOptions(Flags{}, true)
	require.NoError(t, err)
	assert.True(t, opts.System)
}

func TestRenderAutostartLine(t *testing.T) {
	t.Run("enabled prints enabled and not degraded", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderAutostartLine(&buf, true)
		assert.False(t, degraded)
		assert.Contains(t, buf.String(), "enabled")
	})
	t.Run("disabled prints warning and is degraded", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderAutostartLine(&buf, false)
		assert.True(t, degraded)
		assert.Contains(t, buf.String(), "DISABLED")
	})
}

func TestRenderRunningLine(t *testing.T) {
	t.Run("running yes is not degraded", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderRunningLine(&buf, true)
		assert.False(t, degraded)
		assert.Contains(t, buf.String(), "yes")
	})
	t.Run("not running is degraded", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderRunningLine(&buf, false)
		assert.True(t, degraded)
		assert.Contains(t, buf.String(), "no")
	})
}

func TestRenderUnitFileLine(t *testing.T) {
	t.Run("matches when hashes equal", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderUnitFileLine(&buf, autostart.Status{
			UnitPath:           "/etc/systemd/runwisp.service",
			UnitConfigHash:     "abc",
			ExpectedConfigHash: "abc",
		})
		assert.False(t, degraded)
		assert.Contains(t, buf.String(), "matches recorded settings")
	})
	t.Run("drift when hashes differ", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderUnitFileLine(&buf, autostart.Status{
			UnitPath:           "/etc/systemd/runwisp.service",
			UnitConfigHash:     "abc",
			ExpectedConfigHash: "xyz",
		})
		assert.True(t, degraded)
		assert.Contains(t, buf.String(), "DRIFT")
	})
	t.Run("no drift when hashes empty", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderUnitFileLine(&buf, autostart.Status{
			UnitPath: "/some/path",
		})
		assert.False(t, degraded)
		assert.Contains(t, buf.String(), "matches recorded settings")
	})
}

func TestBinaryNote(t *testing.T) {
	t.Run("missing binary", func(t *testing.T) {
		note, degraded := binaryNote(autostart.Status{BinaryExists: false})
		assert.True(t, degraded)
		assert.Contains(t, note, "missing")
	})
	t.Run("sha drift", func(t *testing.T) {
		note, degraded := binaryNote(autostart.Status{
			BinaryExists:      true,
			ExpectedBinarySHA: "aaa",
			BinaryOnDiskSHA:   "bbb",
		})
		assert.True(t, degraded)
		assert.Contains(t, note, "BINARY CHANGED")
	})
	t.Run("matching sha", func(t *testing.T) {
		note, degraded := binaryNote(autostart.Status{
			BinaryExists:      true,
			ExpectedBinarySHA: "aaa",
			BinaryOnDiskSHA:   "aaa",
		})
		assert.False(t, degraded)
		assert.Empty(t, note)
	})
	t.Run("missing recorded sha is benign", func(t *testing.T) {
		note, degraded := binaryNote(autostart.Status{BinaryExists: true})
		assert.False(t, degraded)
		assert.Empty(t, note)
	})
}

func TestRenderBinaryLine_IncludesPath(t *testing.T) {
	var buf bytes.Buffer
	degraded := renderBinaryLine(&buf, autostart.Status{
		Binary:       "/usr/local/bin/runwisp",
		BinaryExists: true,
	})
	assert.False(t, degraded)
	assert.Contains(t, buf.String(), "/usr/local/bin/runwisp")
}

func TestDataDirNote(t *testing.T) {
	t.Run("not writable", func(t *testing.T) {
		note, degraded := dataDirNote(autostart.Status{DataDirWritable: false})
		assert.True(t, degraded)
		assert.Contains(t, note, "not writable")
	})
	t.Run("with last write", func(t *testing.T) {
		when := time.Date(2026, 5, 22, 10, 30, 0, 0, time.UTC)
		note, degraded := dataDirNote(autostart.Status{
			DataDirWritable:  true,
			DataDirLastWrite: when,
		})
		assert.False(t, degraded)
		assert.Contains(t, note, "2026-05-22 10:30:00")
	})
	t.Run("writable no last write", func(t *testing.T) {
		note, degraded := dataDirNote(autostart.Status{DataDirWritable: true})
		assert.False(t, degraded)
		assert.Empty(t, note)
	})
}

func TestRenderDataDirLine_IncludesPath(t *testing.T) {
	var buf bytes.Buffer
	renderDataDirLine(&buf, autostart.Status{
		DataDir:         "/var/lib/runwisp",
		DataDirWritable: true,
	})
	assert.Contains(t, buf.String(), "/var/lib/runwisp")
}

func TestRenderLingerLine(t *testing.T) {
	t.Run("non-linux skips entirely", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderLingerLine(&buf, autostart.Status{OS: "darwin"})
		assert.False(t, degraded)
		assert.Empty(t, buf.String())
	})
	t.Run("linux linger on", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderLingerLine(&buf, autostart.Status{OS: "linux", Linger: true})
		assert.False(t, degraded)
		assert.Contains(t, buf.String(), "on")
	})
	t.Run("linux linger off", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderLingerLine(&buf, autostart.Status{OS: "linux", Linger: false})
		assert.True(t, degraded)
		assert.Contains(t, buf.String(), "OFF")
	})
}

func TestRenderCronLine(t *testing.T) {
	t.Run("no marker, omitted entirely", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderCronLine(&buf, autostart.Status{})
		assert.False(t, degraded)
		assert.Empty(t, buf.String())
	})
	t.Run("masked by this instance, healthy", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderCronLine(&buf, autostart.Status{CronUnit: "cron.service", CronMasked: true})
		assert.False(t, degraded)
		assert.Contains(t, buf.String(), "masked by this instance")
	})
	t.Run("active again, double-fire warning", func(t *testing.T) {
		var buf bytes.Buffer
		degraded := renderCronLine(&buf, autostart.Status{CronUnit: "cron.service", CronActive: true})
		assert.True(t, degraded)
		assert.Contains(t, buf.String(), "firing twice")
	})
}

func TestRenderLastStartLine(t *testing.T) {
	t.Run("zero time skips", func(t *testing.T) {
		var buf bytes.Buffer
		renderLastStartLine(&buf, autostart.Status{})
		assert.Empty(t, buf.String())
	})
	t.Run("non-zero time renders", func(t *testing.T) {
		var buf bytes.Buffer
		when := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
		renderLastStartLine(&buf, autostart.Status{LastStart: when})
		assert.Contains(t, buf.String(), "Last start")
		assert.Contains(t, buf.String(), "2026-05-22")
	})
}

func TestRenderLogsHintLine(t *testing.T) {
	t.Run("empty hint skips", func(t *testing.T) {
		var buf bytes.Buffer
		renderLogsHintLine(&buf, autostart.Status{})
		assert.Empty(t, buf.String())
	})
	t.Run("non-empty hint renders", func(t *testing.T) {
		var buf bytes.Buffer
		renderLogsHintLine(&buf, autostart.Status{LogsHint: "journalctl --user -u runwisp.service"})
		assert.Contains(t, buf.String(), "journalctl")
	})
}

func TestRenderServiceStatus_NotInstalled(t *testing.T) {
	var buf bytes.Buffer
	exit := renderServiceStatus(&buf, autostart.Status{
		UnitExists: false,
		UnitPath:   "/etc/systemd/runwisp.service",
	})
	assert.Equal(t, serviceStatusExitNotInstalled, exit)
	assert.Contains(t, buf.String(), "Installed:  no")
	assert.Contains(t, buf.String(), "service install")
}

func TestRenderServiceStatus_Unmanaged(t *testing.T) {
	var buf bytes.Buffer
	exit := renderServiceStatus(&buf, autostart.Status{
		UnitExists:  true,
		UnitManaged: false,
		UnitPath:    "/etc/systemd/runwisp.service",
	})
	assert.Equal(t, serviceStatusExitDegraded, exit)
	assert.Contains(t, buf.String(), "conflict")
}

func TestRenderServiceStatus_HealthyLinux(t *testing.T) {
	var buf bytes.Buffer
	exit := renderServiceStatus(&buf, autostart.Status{
		OS:                 "linux",
		UnitExists:         true,
		UnitManaged:        true,
		UnitPath:           "/etc/systemd/runwisp.service",
		Autostart:          true,
		Running:            true,
		Binary:             "/usr/bin/runwisp",
		BinaryExists:       true,
		ExpectedConfigHash: "abc",
		UnitConfigHash:     "abc",
		ExpectedBinarySHA:  "sha",
		BinaryOnDiskSHA:    "sha",
		DataDir:            "/var/lib/runwisp",
		DataDirWritable:    true,
		Linger:             true,
		LogsHint:           "journalctl --user -u runwisp.service",
	})
	assert.Equal(t, serviceStatusExitHealthy, exit)
	out := buf.String()
	assert.Contains(t, out, "Installed:  yes")
	assert.Contains(t, out, "Autostart:  enabled")
	assert.Contains(t, out, "Running:    yes")
}

func TestRenderServiceStatus_DegradedDrift(t *testing.T) {
	var buf bytes.Buffer
	exit := renderServiceStatus(&buf, autostart.Status{
		OS:                 "linux",
		UnitExists:         true,
		UnitManaged:        true,
		UnitPath:           "/etc/systemd/runwisp.service",
		Autostart:          true,
		Running:            true,
		Binary:             "/usr/bin/runwisp",
		BinaryExists:       true,
		ExpectedConfigHash: "abc",
		UnitConfigHash:     "different",
		DataDir:            "/var/lib/runwisp",
		DataDirWritable:    true,
		Linger:             true,
	})
	assert.Equal(t, serviceStatusExitDegraded, exit)
	assert.Contains(t, buf.String(), "DRIFT")
}
