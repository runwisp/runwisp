// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/importer"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func taskNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Tasks))
	for i := range cfg.Tasks {
		names = append(names, cfg.Tasks[i].Name)
	}
	return names
}

func loadedTask(t *testing.T, cfg *config.Config, name string) model.Task {
	t.Helper()
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Name == name {
			return cfg.Tasks[i]
		}
	}
	t.Fatalf("task %q not found; have %v", name, taskNames(cfg))
	return model.Task{}
}

// importTwoTier runs `import cron --write` against a config path, the primary
// two-tier entry point exercised end-to-end.
func importTwoTier(t *testing.T, cfgPath, crontab string) (stderr string, err error) {
	t.Helper()
	src := tempFile(t, "crontab", crontab)
	var out, errb bytes.Buffer
	err = runImportCron(&out, &errb, openTempFile(t, ""), src,
		importer.CronOptions{}, Flags{CfgFile: cfgPath}, importOpts{write: true})
	return errb.String(), err
}

// importSupervisordTwoTier runs `import supervisord --write` against a config
// path.
func importSupervisordTwoTier(t *testing.T, cfgPath, conf string) (stderr string, err error) {
	t.Helper()
	src := tempFile(t, "supervisord.conf", conf)
	var out, errb bytes.Buffer
	err = runImportSupervisord(&out, &errb, openTempFile(t, ""), []string{src},
		Flags{CfgFile: cfgPath}, importOpts{write: true})
	return errb.String(), err
}

// TestImportCronTwoTierNamesAPreexistingBreakageToo covers the one write that
// skips the load gate: the import has its own `# TODO`, so the files are kept for
// the operator to fix. Without this, a root config that was already broken went
// unmentioned — they'd resolve every TODO, run validate, and be told about
// something they never touched.
func TestImportCronTwoTierNamesAPreexistingBreakageToo(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	// Valid TOML, invalid config: a task with a schedule and no command.
	require.NoError(t, os.WriteFile(cfgPath, []byte("[tasks.web]\ncron = \"@daily\"\n"), 0o600))

	// An unparseable cron expression is what makes the import itself carry a TODO.
	stderr, err := importTwoTier(t, cfgPath, "99 99 * * * /bin/bad\n")
	require.NoError(t, err)

	assert.Contains(t, stderr, "Resolve the # TODO items in")
	assert.Contains(t, stderr, "didn't load before this import either:")
	assert.FileExists(t, filepath.Join(dir, "runwisp.d", "imported.toml"),
		"the files are kept precisely so the operator can fix them in place")
}

func TestImportCronTwoTierGreenfield(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")

	stderr, err := importTwoTier(t, cfgPath, "30 2 * * * /usr/bin/backup.sh --full\n")
	require.NoError(t, err)

	rootBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(rootBytes), `include = ["runwisp.d/*.toml"]`)
	assert.NotContains(t, string(rootBytes), "[tasks.backup]", "the task belongs in staging, not the root")

	stagingBytes, err := os.ReadFile(filepath.Join(dir, "runwisp.d", "imported.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(stagingBytes), "[tasks.backup]")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.True(t, loadedTask(t, cfg, "backup").Staged, "imported task should be marked staged")
	assert.Contains(t, stderr, "runwisp promote")
}

func TestImportCronTwoTierBrownfieldWiresInclude(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("[tasks.native]\nrun = \"echo native\"\n"), 0o644))

	_, err := importTwoTier(t, cfgPath, "0 3 * * * /usr/bin/imported.sh\n")
	require.NoError(t, err)

	rootBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(rootBytes), "[tasks.native]", "existing root task must be preserved")
	assert.Contains(t, string(rootBytes), `include = ["runwisp.d/*.toml"]`)

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"native", "imported"}, taskNames(cfg))
	assert.False(t, loadedTask(t, cfg, "native").Staged)
	assert.True(t, loadedTask(t, cfg, "imported").Staged)
}

func TestImportCronTwoTierAlreadyIncludesLeavesRootByteIdentical(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	orig := "[daemon]\ninclude = [\"runwisp.d/*.toml\"]\n\n[tasks.native]\nrun = \"echo native\"\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(orig), 0o644))

	stderr, err := importTwoTier(t, cfgPath, "0 3 * * * /usr/bin/imported.sh\n")
	require.NoError(t, err)

	rootBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, orig, string(rootBytes), "an already-covering root must not be rewritten")
	assert.Contains(t, stderr, "already loads runwisp.d")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"native", "imported"}, taskNames(cfg))
}

func TestImportCronTwoTierRefusesCustomInclude(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	orig := "[daemon]\ninclude = [\"other/*.toml\"]\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(orig), 0o644))

	_, err := importTwoTier(t, cfgPath, "0 3 * * * /usr/bin/imported.sh\n")
	require.Error(t, err)
	u, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, u.title, "custom [daemon].include")

	rootBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, orig, string(rootBytes), "root must be untouched on refusal")
	_, statErr := os.Stat(filepath.Join(dir, "runwisp.d", "imported.toml"))
	assert.True(t, os.IsNotExist(statErr), "staging file must not be created on refusal")
}

func TestImportCronTwoTierReimportSkipsSameCommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	// A promoted job: backup lives natively in the root, which already includes runwisp.d.
	root := "[daemon]\ninclude = [\"runwisp.d/*.toml\"]\n\n" +
		"[tasks.backup]\ncron = \"30 2 * * *\"\nrun = \"/usr/bin/backup.sh --full\"\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(root), 0o644))

	stderr, err := importTwoTier(t, cfgPath, "30 2 * * * /usr/bin/backup.sh --full\n")
	require.NoError(t, err)
	assert.Contains(t, stderr, "already defined in runwisp.toml with the same command")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"backup"}, taskNames(cfg), "no duplicate backup created")
	assert.False(t, loadedTask(t, cfg, "backup").Staged, "the surviving backup is the native one")
}

func TestImportCronTwoTierReimportRenamesDifferentCommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	root := "[daemon]\ninclude = [\"runwisp.d/*.toml\"]\n\n" +
		"[tasks.backup]\ncron = \"0 0 * * *\"\nrun = \"/opt/something-else\"\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(root), 0o644))

	stderr, err := importTwoTier(t, cfgPath, "30 2 * * * /usr/bin/backup.sh --full\n")
	require.NoError(t, err)
	assert.Contains(t, stderr, "imported this one as")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"backup", "backup-2"}, taskNames(cfg))
	assert.False(t, loadedTask(t, cfg, "backup").Staged)
	assert.True(t, loadedTask(t, cfg, "backup-2").Staged)
}

func TestImportCronTwoTierContentErrorKeepsFiles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")

	stderr, err := importTwoTier(t, cfgPath, "99 99 * * * /usr/bin/broken.sh\n")
	require.NoError(t, err) // a TODO is not a hard failure — the operator fixes it in place

	stagingBytes, err := os.ReadFile(filepath.Join(dir, "runwisp.d", "imported.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(stagingBytes), "TODO")
	_, err = os.Stat(cfgPath)
	require.NoError(t, err, "root config should still be created")
	assert.Contains(t, stderr, "needs a fix before this config loads")
}

// TestStageImportReportsConflict drives the writer with staging content that
// duplicates a root task name (bypassing the importer's dedup) to prove the
// merged-load failure surfaces as a conflict and rolls both files back rather
// than leaving a broken config. configedit owns the rollback itself; what's
// asserted here is that the CLI names the right cause.
func TestStageImportReportsConflict(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	orig := "[tasks.dup]\nrun = \"echo original\"\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(orig), 0o644))

	staging := config.SchemaDirective + "[tasks.dup]\ncron = \"0 0 * * *\"\nrun = \"echo dupe\"\n"
	res, err := importer.ParseCrontab(strings.NewReader("0 0 * * * echo dupe\n"), importer.CronOptions{})
	require.NoError(t, err)

	var stderr bytes.Buffer
	rep := importReport{res: res, source: sourceCrontab}
	err = stageImport(&stderr, cfgPath, staging, rep, importOpts{})
	require.Error(t, err)
	u, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, u.title, "conflicts")

	rootBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, orig, string(rootBytes), "root must be rolled back to its original bytes")
	_, statErr := os.Stat(filepath.Join(dir, "runwisp.d", "imported.toml"))
	assert.True(t, os.IsNotExist(statErr), "staging file must be rolled back")
}

// TestImportCronTwoTierAlreadyBrokenConfigIsNotBlamedOnTheImport covers the
// difference between "your import clashes with your config" and "your config was
// already broken". Both roll back, but only one of them is the import's fault,
// and telling the operator the wrong one sends them hunting for a clash that
// doesn't exist.
func TestImportCronTwoTierAlreadyBrokenConfigIsNotBlamedOnTheImport(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	orig := "[tasks.broken]\ncron = \"not a cron expression\"\nrun = \"echo hi\"\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(orig), 0o644))

	_, err := importTwoTier(t, cfgPath, "0 3 * * * /usr/bin/imported.sh\n")
	require.Error(t, err)
	u, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, u.title, "didn't load before this import either")
	assert.NotContains(t, u.title, "conflicts")

	rootBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, orig, string(rootBytes))
}

// TestImportSupervisordTwoTierReimportSkipsPromoted is the supervisord half of
// identity-aware dedup. Before it existed, re-importing a supervisord config
// after promoting one of its programs failed the merged load and rolled the whole
// import back — while the identical cron flow worked.
func TestImportSupervisordTwoTierReimportSkipsPromoted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	// A promoted program: web lives natively in the root, which already includes runwisp.d.
	root := "[daemon]\ninclude = [\"runwisp.d/*.toml\"]\n\n" +
		"[services.web]\nrun = \"/usr/bin/gunicorn app:app\"\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(root), 0o644))

	stderr, err := importSupervisordTwoTier(t, cfgPath,
		"[program:web]\ncommand=/usr/bin/gunicorn app:app\n\n[program:worker]\ncommand=/usr/bin/worker\n")
	require.NoError(t, err)
	assert.Contains(t, stderr, "already defined in runwisp.toml with the same command")

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"web", "worker"}, taskNames(cfg), "no duplicate web created")
	assert.False(t, loadedTask(t, cfg, "web").Staged, "the surviving web is the native one")
	assert.True(t, loadedTask(t, cfg, "worker").Staged)
}

// TestImportTwoTierPreservesRestrictiveConfigMode covers an operator who locked
// their runwisp.toml down because it carries inline secrets. Wiring the include
// must not widen it to the world-readable default.
func TestImportTwoTierPreservesRestrictiveConfigMode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("[tasks.native]\nrun = \"echo native\"\n"), 0o600))

	_, err := importTwoTier(t, cfgPath, "0 3 * * * /usr/bin/imported.sh\n")
	require.NoError(t, err)

	info, err := os.Stat(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
