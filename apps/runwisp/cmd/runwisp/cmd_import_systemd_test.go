// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeUnitDir writes systemd units (relative path → body) into a temp dir,
// points the bare-name lookup at it, and returns it.
func writeUnitDir(t *testing.T, units map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range units {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	prev := systemdUnitDirs
	t.Cleanup(func() { systemdUnitDirs = prev })
	systemdUnitDirs = []string{dir}
	return dir
}

const (
	oneshotUnit = "[Service]\nType=oneshot\nExecStart=/bin/true\n"
	dailyTimer  = "[Timer]\nOnCalendar=daily\n"
)

func importSystemd(t *testing.T, args []string, f Flags, opts importOpts) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = runImportSystemd(&out, &errOut, openTempFile(t, ""), args, f, opts)
	return out.String(), errOut.String(), err
}

// `import systemd /etc/systemd/system/backup.*` matched backup.service.d and
// failed with "is a directory". The drop-in dir is read with its unit anyway.
func TestImportSystemdSkipsDropInDirArgs(t *testing.T) {
	dir := writeUnitDir(t, map[string]string{
		"backup.timer":              dailyTimer,
		"backup.service":            oneshotUnit,
		"backup.service.d/env.conf": "[Service]\nEnvironment=EXTRA=1\n",
	})
	stdout, _, err := importSystemd(t, []string{
		filepath.Join(dir, "backup.service"), filepath.Join(dir, "backup.service.d"), filepath.Join(dir, "backup.timer"),
	}, Flags{}, importOpts{})
	require.NoError(t, err)
	assert.Contains(t, stdout, `EXTRA = "1"`)
}

// The footer said `systemctl disable --now <unit>`; run on the service of a
// timer pair it does nothing, and the timer keeps firing.
func TestImportSystemdNamesTheUnitsToDisable(t *testing.T) {
	writeUnitDir(t, map[string]string{
		"a.timer": dailyTimer, "a.service": oneshotUnit,
		"b.timer": dailyTimer, "b.service": oneshotUnit,
		"web.service": "[Service]\nExecStart=/bin/web\n",
	})
	_, stderr, err := importSystemd(t, []string{"a.timer", "b.timer", "web.service"}, Flags{}, importOpts{})
	require.NoError(t, err)
	assert.Contains(t, stderr, "sudo systemctl disable --now a.timer b.timer web.service")
	assert.NotContains(t, stderr, "<unit>")
	assert.Contains(t, stderr, "Imported systemd units → 2 tasks, 1 service")
}

// One timer without its service refused the whole batch with "Fix that line in
// the source", leaving the operator to find which unit to leave out.
func TestImportSystemdWriteRefusalNamesTheUnitThatCantLoad(t *testing.T) {
	writeUnitDir(t, map[string]string{"good.timer": dailyTimer, "good.service": oneshotUnit, "orphan.timer": dailyTimer})
	cfgPath := filepath.Join(t.TempDir(), "runwisp.toml")

	_, stderr, err := importSystemd(t, []string{"good.timer", "orphan.timer"}, Flags{CfgFile: cfgPath}, importOpts{write: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Can't load: orphan (orphan.timer).")
	assert.NotContains(t, err.Error(), "good")
	assert.Contains(t, stderr, "1 item needs review; orphan can't load until fixed")
	assert.NoFileExists(t, cfgPath)
}

// Moving a few units at a time used to delete the units staged before: the
// second `--write` rewrote runwisp.d/imported.toml with only its own tasks.
func TestImportSystemdWriteKeepsUnitsStagedEarlier(t *testing.T) {
	writeUnitDir(t, map[string]string{
		"first.timer": dailyTimer, "first.service": oneshotUnit,
		"second.timer": dailyTimer, "second.service": oneshotUnit,
	})
	cfgPath := filepath.Join(t.TempDir(), "runwisp.toml")
	f := Flags{CfgFile: cfgPath}

	_, _, err := importSystemd(t, []string{"first.timer"}, f, importOpts{write: true})
	require.NoError(t, err)
	_, stderr, err := importSystemd(t, []string{"second.timer"}, f, importOpts{write: true, dryRun: true})
	require.NoError(t, err)
	assert.Contains(t, stderr, "Would keep 1 entry from earlier imports: first.")
	_, stderr, err = importSystemd(t, []string{"second.timer"}, f, importOpts{write: true})
	require.NoError(t, err)
	assert.Contains(t, stderr, "Kept 1 entry from earlier imports: first.")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	var names []string
	for _, task := range cfg.Tasks {
		names = append(names, task.Name)
	}
	assert.ElementsMatch(t, []string{"first", "second"}, names)
}

// An import as a normal user left an empty .runwisp/ data dir in the working
// directory, even when it wrote nothing: the root pre-run made one for every
// subcommand.
func TestImportMakesNoDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	prev := flags
	t.Cleanup(func() { flags = prev })
	flags.DataDir = ""

	var preRun func(*cobra.Command, []string) error
	for c := importSystemdCmd; c != nil && preRun == nil; c = c.Parent() {
		preRun = c.PersistentPreRunE
	}
	require.NotNil(t, preRun)
	require.NoError(t, preRun(importSystemdCmd, nil))
	assert.NoDirExists(t, filepath.Join(dir, ".runwisp"))
}
