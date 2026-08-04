// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/importer"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// importDry runs one dry-run import and returns what it said on stderr. stdout
// is captured separately because in stdout mode --dry-run is a no-op and the
// TOML still has to arrive.
func importDry(t *testing.T, cfgPath, crontab string, opts importOpts) (stdout, stderr string, err error) {
	t.Helper()
	opts.dryRun = true
	src := tempFile(t, "crontab", crontab)
	var out, errb bytes.Buffer
	err = runImportCron(&out, &errb, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{CfgFile: cfgPath}, opts)
	return out.String(), errb.String(), err
}

const dryCrontab = "0 3 * * * /usr/local/bin/backup.sh --full\n"

// TestImportDryRunGreenfieldWritesNothing is the whole promise of the flag on
// the path that would otherwise create two files and a directory.
func TestImportDryRunGreenfieldWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")

	_, stderr, err := importDry(t, cfgPath, dryCrontab, importOpts{write: true})
	require.NoError(t, err)

	assert.Contains(t, stderr, "Would stage 1 task in "+filepath.Join(dir, "runwisp.d", "imported.toml"))
	assert.Contains(t, stderr, "Would create "+cfgPath+" and wire it to load runwisp.d/*.toml.")
	assert.Contains(t, stderr, "Nothing was written.")
	assert.NotContains(t, stderr, "runwisp promote", "nothing is staged yet, so there's nothing to promote")

	require.NoFileExists(t, cfgPath)
	require.NoFileExists(t, filepath.Join(dir, "runwisp.d", "imported.toml"))
	assert.NoDirExists(t, filepath.Join(dir, "runwisp.d"))
}

// TestImportDryRunLeavesAnExistingConfigByteIdentical is the case an operator
// actually reaches for: a config they already care about, and a dry run that
// must not so much as rewrite the include line it would have added.
func TestImportDryRunLeavesAnExistingConfigByteIdentical(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte("[tasks.web]\nrun = \"/usr/bin/web\"\ncron = \"@daily\"\n"), 0o600))
	before := testutil.SnapshotTree(t, dir)

	_, stderr, err := importDry(t, cfgPath, dryCrontab, importOpts{write: true})
	require.NoError(t, err)

	assert.Contains(t, stderr, "Would wire "+cfgPath+" to load runwisp.d/*.toml.")
	assert.Equal(t, before, testutil.SnapshotTree(t, dir), "a dry run must leave the tree byte-identical")
}

// TestImportDryRunNamesWhatItDidNotCheck is the honesty rule applied to the
// feature's own limits: a plan with nothing flagged reads as a guarantee, and the
// merge is the one thing it can't prove without writing the files. Saying so is
// what keeps a clean dry run from over-promising.
func TestImportDryRunNamesWhatItDidNotCheck(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte("[tasks.web]\nrun = \"/usr/bin/web\"\ncron = \"@daily\"\n"), 0o600))

	_, stderr, err := importDry(t, cfgPath, dryCrontab, importOpts{write: true})
	require.NoError(t, err)
	assert.Contains(t, stderr, "Not checked: whether these merge with the tasks you already have.")

	// It stays quiet when there's already something concrete to report — a caveat
	// stacked under a real failure is noise, and the failure is the news.
	_, stderr, err = importDry(t, cfgPath, "99 99 * * * /bin/bad\n", importOpts{write: true})
	require.NoError(t, err)
	assert.NotContains(t, stderr, "Not checked:")
}

// TestImportDryRunReportsAnAlreadyWiredRoot pins the third mood: a root that
// needs no change says so in the same words the real write uses, because
// "already loads" is tenseless.
func TestImportDryRunReportsAnAlreadyWiredRoot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte("[daemon]\ninclude = [\"runwisp.d/*.toml\"]\n"), 0o600))

	_, stderr, err := importDry(t, cfgPath, dryCrontab, importOpts{write: true})
	require.NoError(t, err)
	assert.Contains(t, stderr, cfgPath+" already loads runwisp.d/*.toml.")
	assert.NotContains(t, stderr, "Would wire")
}

// TestImportDryRunFlagsARootThatAlreadyFails is the one merge failure a dry run
// can see coming. Saying it here is what stops the operator from dropping
// --dry-run only to be told their config was broken before they started.
func TestImportDryRunFlagsARootThatAlreadyFails(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	// Valid TOML that fails validation: a task with a schedule and no command.
	require.NoError(t, os.WriteFile(cfgPath, []byte("[tasks.web]\ncron = \"@daily\"\n"), 0o600))

	_, stderr, err := importDry(t, cfgPath, dryCrontab, importOpts{write: true})
	require.NoError(t, err, "a broken root is something to report, not to fail the dry run over")
	assert.Contains(t, stderr, "runwisp.toml doesn't load as it stands, so a real run would refuse:")
	assert.Contains(t, stderr, "Nothing was written.")
}

// TestImportDryRunFailsOnAnUnparseableRoot is the line between the two: a root
// that fails *validation* is reported and the dry run continues, but one that
// isn't TOML at all can't be planned, because wiring the include is a surgical
// text edit of that file. A real run stops in exactly the same place, which is
// the answer the dry run owes.
func TestImportDryRunFailsOnAnUnparseableRoot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("this is not toml at all\n"), 0o600))
	before := testutil.SnapshotTree(t, dir)

	_, _, err := importDry(t, cfgPath, dryCrontab, importOpts{write: true})
	require.Error(t, err)
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe), "should be a user-facing error, got %T", err)
	assert.Equal(t, "can't update runwisp.toml", ufe.title)
	assert.Equal(t, before, testutil.SnapshotTree(t, dir))
}

// TestImportDryRunStillReportsABrokenJob is directive #1 under the flag: a dry
// run that hides the thing needing a fix is worse than no dry run, because the
// operator drops the flag believing it's clean.
func TestImportDryRunStillReportsABrokenJob(t *testing.T) {
	dir := t.TempDir()
	_, stderr, err := importDry(t, filepath.Join(dir, "runwisp.toml"),
		"99 99 * * * /bin/bad\n", importOpts{write: true})
	require.NoError(t, err)

	assert.Contains(t, stderr, "1 item needs a fix before this config loads.")
	assert.Contains(t, stderr, "cron expression 99 99 * * * didn't parse")
	assert.Contains(t, stderr, "Nothing was written.")
}

// TestImportDryRunToAnOutputPath covers -o: one file named, and nothing at it.
func TestImportDryRunToAnOutputPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.toml")

	_, stderr, err := importDry(t, "", dryCrontab, importOpts{output: target})
	require.NoError(t, err)
	assert.Contains(t, stderr, "Would write "+target+".")
	assert.Contains(t, stderr, "Nothing was written.")
	require.NoFileExists(t, target)
}

// TestImportDryRunToAnExistingOutputPath is why the dry run doesn't just borrow
// confirmAndWrite's refusal: it isn't writing, so an existing target is
// information, not an error. It still names the flag a real run would need.
func TestImportDryRunToAnExistingOutputPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.toml")
	require.NoError(t, os.WriteFile(target, []byte("# mine\n"), 0o600))

	_, stderr, err := importDry(t, "", dryCrontab, importOpts{output: target})
	require.NoError(t, err, "an existing target must not fail a dry run")
	assert.Contains(t, stderr, "Would overwrite "+target+" — a real run needs --force (or -o elsewhere).")

	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "# mine\n", string(body))

	// With --force there is nothing to warn about, because the real run would go
	// through.
	_, stderr, err = importDry(t, "", dryCrontab, importOpts{output: target, force: true})
	require.NoError(t, err)
	assert.Contains(t, stderr, "Would write "+target+".")
	assert.NotContains(t, stderr, "--force")
}

// TestImportDryRunInStdoutModeStillPrintsTheTOML is the deliberate no-op: piping
// the TOML somewhere writes nothing either way, so --dry-run must not turn
// `runwisp import cron --dry-run > x.toml` into an empty file.
func TestImportDryRunInStdoutModeStillPrintsTheTOML(t *testing.T) {
	stdout, stderr, err := importDry(t, "", dryCrontab, importOpts{})
	require.NoError(t, err)
	assert.Contains(t, stdout, "[tasks.backup]")
	assert.Contains(t, stderr, "Review the TOML above")
	assert.NotContains(t, stderr, "Nothing was written.")
}

// TestImportDryRunRejectsQuiet: a dry run's only output is the summary, so
// silencing it leaves a command that reads a file and does nothing at all.
func TestImportDryRunRejectsQuiet(t *testing.T) {
	_, _, err := importDry(t, "", dryCrontab, importOpts{write: true, quiet: true})
	require.Error(t, err)

	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe), "should be a user-facing error, got %T", err)
	assert.Equal(t, "--dry-run and --quiet contradict each other", ufe.title)
}

// TestImportSupervisordDryRunWritesNothing keeps the flag from being a
// cron-only feature: it is registered on both subcommands, and both check it.
func TestImportSupervisordDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	src := tempFile(t, "supervisord.conf", "[program:web]\ncommand=/usr/bin/web\n")

	var out, errb bytes.Buffer
	err := runImportSupervisord(&out, &errb, openTempFile(t, ""), []string{src},
		Flags{CfgFile: cfgPath}, importOpts{write: true, dryRun: true})
	require.NoError(t, err)

	assert.Contains(t, errb.String(), "Would stage 1 service in ")
	assert.Contains(t, errb.String(), "Nothing was written.")
	require.NoFileExists(t, cfgPath)

	errb.Reset()
	err = runImportSupervisord(&out, &errb, openTempFile(t, ""), []string{src},
		Flags{CfgFile: cfgPath}, importOpts{write: true, dryRun: true, quiet: true})
	require.Error(t, err, "the contradiction is rejected on both subcommands")
}
