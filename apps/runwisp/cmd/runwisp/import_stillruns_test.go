// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// importCron runs one import however opts says to deliver it, and returns
// stderr. It exists so the still-runs tests can walk every delivery path with
// the same crontab — the warning is only true if all of them say it.
func importCron(t *testing.T, cfgPath, crontab string, opts importOpts) string {
	t.Helper()
	src := tempFile(t, "crontab", crontab)
	var out, errb bytes.Buffer
	require.NoError(t, runImportCron(&out, &errb, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{CfgFile: cfgPath}, opts))
	return errb.String()
}

// TestImportWarnsThatCronStillRunsTheJobs is the duplicate-execution hazard.
//
// An import *copies* definitions; it disables nothing. So the moment the daemon
// starts, every job it imported fires twice — once from cron, once from RunWisp
// — and nothing else in the output implies otherwise ("Validated", "Wrote",
// "runwisp promote" all read like the move is complete). For a backup or a
// `find -delete` that's real damage, so every delivery path has to say it.
func TestImportWarnsThatCronStillRunsTheJobs(t *testing.T) {
	const want = "cron still runs these jobs."

	t.Run("write", func(t *testing.T) {
		dir := t.TempDir()
		stderr, err := importTwoTier(t, filepath.Join(dir, "runwisp.toml"), dryCrontab)
		require.NoError(t, err)
		assert.Contains(t, stderr, want)
		assert.Contains(t, stderr, "or each one runs twice.")
	})

	t.Run("dry run", func(t *testing.T) {
		dir := t.TempDir()
		_, stderr, err := importDry(t, filepath.Join(dir, "runwisp.toml"), dryCrontab, importOpts{write: true})
		require.NoError(t, err)
		assert.Contains(t, stderr, want)
	})

	t.Run("output path", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "out.toml")
		assert.Contains(t, importCron(t, "", dryCrontab, importOpts{output: target}), want)
	})

	t.Run("stdout", func(t *testing.T) {
		// Nothing is installed yet on this path, but the advice is still "before
		// starting RunWisp" — and an operator who pipes to runwisp.toml and starts the
		// daemon has taken the same risk by a shorter route.
		assert.Contains(t, importCron(t, "", dryCrontab, importOpts{}), want)
	})
}

// TestImportSupervisordWarnsItStillManagesThem: same hazard, different words,
// because "comment it out of your crontab" is not advice a supervisord operator
// can follow. The two wordings live on importSource so a third importer can't
// pick up the label and silently inherit cron's instructions.
func TestImportSupervisordWarnsItStillManagesThem(t *testing.T) {
	dir := t.TempDir()
	stderr, err := importSupervisordTwoTier(t, filepath.Join(dir, "runwisp.toml"),
		"[program:web]\ncommand=/usr/bin/web\n")
	require.NoError(t, err)

	assert.Contains(t, stderr, "supervisord still manages these programs.")
	assert.NotContains(t, stderr, "crontab -e", "cron's fix doesn't apply here")
}

// TestImportDoesNotWarnWithNothingToDuplicate keeps the warning from crying wolf.
// It's a statement about jobs RunWisp is now also running, so with no such jobs
// — nothing imported, or a config that won't load — it isn't true yet, and an
// operator who's already been handed a fix-this-first has no use for it.
func TestImportDoesNotWarnWithNothingToDuplicate(t *testing.T) {
	const want = "runs twice"

	t.Run("empty crontab", func(t *testing.T) {
		dir := t.TempDir()
		stderr, err := importTwoTier(t, filepath.Join(dir, "runwisp.toml"), "# just a comment\n")
		require.NoError(t, err)
		assert.NotContains(t, stderr, want)
	})

	t.Run("config that won't load", func(t *testing.T) {
		dir := t.TempDir()
		stderr, err := importTwoTier(t, filepath.Join(dir, "runwisp.toml"), "99 99 * * * /bin/bad\n")
		require.NoError(t, err)
		assert.Contains(t, stderr, "Resolve the # TODO items in")
		assert.NotContains(t, stderr, want)
	})
}
