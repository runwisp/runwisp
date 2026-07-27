// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadTree loads the conventional root config out of a staged file tree.
func loadTree(t *testing.T, dir string) *Config {
	t.Helper()
	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)
	return cfg
}

func TestIncludeCron_TasksBecomeLive(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/backup": "0 3 * * * /usr/local/bin/backup.sh --full\n",
	})

	cfg := loadTree(t, dir)
	task := findTask(t, cfg, "backup")
	assert.Equal(t, "0 3 * * *", task.Cron)
	assert.Equal(t, "/usr/local/bin/backup.sh --full", task.Run)

	// Provenance, so the UI can badge it and `promote` can offer a path out.
	assert.Equal(t, model.SourceCron, task.Source)
	assert.Equal(t, filepath.Join(dir, "crontabs", "backup"), task.SourceFile)
	assert.Equal(t, []string{filepath.Join(dir, "crontabs", "backup")}, cfg.CronFiles())
	assert.Empty(t, cfg.CronFindings)
}

// TestIncludeCron_NativeWinsSameJob is the decision that makes `promote` work: a
// job already in the operator's TOML, verbatim, must not also load from the
// crontab it was promoted out of.
func TestIncludeCron_NativeWinsSameJob(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]

[tasks.backup]
cron = "0 4 * * *"
run = "/usr/local/bin/backup.sh"
description = "promoted, and retimed since"
`,
		"crontabs/backup": "0 3 * * * /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	assert.Equal(t, []string{"backup"}, taskNames(cfg), "the cron copy must not come back as backup-*")

	task := findTask(t, cfg, "backup")
	assert.Equal(t, "0 4 * * *", task.Cron, "the operator's schedule wins, not the crontab's")
	assert.Equal(t, model.SourceNative, task.Source)
}

// TestIncludeCron_RenamesDifferentJob is the other half: cron names are derived
// from the command, so collisions are routine. Dropping on a name match alone
// would silently retire a live job — a rename keeps both running.
func TestIncludeCron_RenamesDifferentJob(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]

[tasks.backup]
cron = "0 4 * * *"
run = "pg_dump mydb"
`,
		"crontabs/db": "0 3 * * * /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	require.Len(t, cfg.Tasks, 2, "both jobs run; got %v", taskNames(cfg))
	assert.Equal(t, "pg_dump mydb", findTask(t, cfg, "backup").Run)

	renamed := findTask(t, cfg, "backup-db")
	assert.Equal(t, "/usr/local/bin/backup.sh", renamed.Run)
	assert.Equal(t, model.SourceCron, renamed.Source)
}

// TestIncludeCron_CollisionSuffixNamesTheSourceFile is the decision-3 guard.
// Cron task names are derived from the command, so two crontabs routinely derive
// the same one. The suffix that resolves the clash must be a function of the
// *source file*, never of the position the file happened to occupy in the glob:
// with positional -2/-3 suffixes, dropping one more colliding crontab onto the
// box renumbers the tasks already running, detaching their run history and
// breaking every notification route that names them.
func TestIncludeCron_CollisionSuffixNamesTheSourceFile(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/alpha": "0 3 * * * /usr/local/bin/backup.sh --alpha\n",
		"crontabs/zeta":  "0 4 * * * /usr/local/bin/backup.sh --zeta\n",
	})
	before := taskNames(loadTree(t, dir))
	assert.ElementsMatch(t, []string{"backup", "backup-zeta"}, before)

	// A third colliding crontab, sorting between the two. Under positional
	// suffixes zeta would slide from backup-2 to backup-3.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crontabs", "mu"),
		[]byte("0 5 * * * /usr/local/bin/backup.sh --mu\n"), 0o600))
	after := taskNames(loadTree(t, dir))

	assert.Subset(t, after, before,
		"adding crontabs/mu renamed a task that was already running: had %v, now %v", before, after)
	assert.ElementsMatch(t, []string{"backup", "backup-mu", "backup-zeta"}, after)
}

// TestIncludeCron_RenameIsReported covers the residual: the *base* name belongs to
// whichever colliding file sorts first, so a newly-added crontab can take it and
// push the incumbent to a suffixed name. That is still better than retiring a live
// job, but an operator who wrote `backup` and finds `backup-zeta` in the UI has to
// be told why — otherwise the name looks like something RunWisp invented.
func TestIncludeCron_RenameIsReported(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/alpha": "0 3 * * * /usr/local/bin/backup.sh --alpha\n",
		"crontabs/zeta":  "0 4 * * * /usr/local/bin/backup.sh --zeta\n",
	})

	cfg := loadTree(t, dir)
	require.Len(t, cfg.CronFindings, 1)
	finding := cfg.CronFindings[0]
	assert.False(t, finding.Skipped, "a renamed job still runs")
	assert.Equal(t, "backup-zeta", finding.Task)
	assert.Equal(t, filepath.Join(dir, "crontabs", "zeta"), finding.File)

	warnings := strings.Join(Warnings(cfg), "\n")
	assert.Contains(t, warnings, "backup-zeta")
}

// TestIncludeCron_BlockedJobSkippedRestKept is the parity decision: crond drops a
// malformed entry and keeps the file, so RunWisp does too — loudly.
func TestIncludeCron_BlockedJobSkippedRestKept(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/mixed": "0 3 * * * /usr/local/bin/good.sh\n99 99 * * * /usr/local/bin/bad.sh\n",
	})

	cfg := loadTree(t, dir)
	assert.Equal(t, []string{"good"}, taskNames(cfg))

	require.Len(t, cfg.CronFindings, 1)
	skip := cfg.CronFindings[0]
	assert.True(t, skip.Skipped)
	assert.Empty(t, skip.Task, "a job that isn't running has no task name")
	assert.Equal(t, filepath.Join(dir, "crontabs", "mixed"), skip.File)
	assert.Equal(t, 2, skip.Line, "the operator needs file:line, not a name they've never seen")
	assert.NotEmpty(t, skip.Source)
	assert.Contains(t, skip.Reason, "didn't parse")

	// A skipped job has no run record, so Warnings is the only place it can
	// surface. If it isn't here, it is invisible.
	warnings := strings.Join(Warnings(cfg), "\n")
	assert.Contains(t, warnings, "crontabs/mixed")
	assert.Contains(t, warnings, "not running")
}

func TestIncludeCron_UnreadableFileIsHardError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/missing"]
`,
	})
	// A glob that matches nothing is fine; a named file that doesn't exist is
	// also just a non-match. What must fail is a file that exists and can't be
	// read — otherwise a permissions slip silently unschedules a whole crontab.
	path := filepath.Join(dir, "crontabs", "missing")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("0 3 * * * /bin/true\n"), 0o000))
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file")
	}

	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

// TestIncludeCron_GlobMatchingNothingIsFine: an empty /etc/cron.d is a normal
// machine, and a glob that matches nothing today may match tomorrow.
func TestIncludeCron_GlobMatchingNothingIsFine(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]

[tasks.native]
run = "echo hi"
`,
	})
	cfg := loadTree(t, dir)
	assert.Equal(t, []string{"native"}, taskNames(cfg))
	assert.Empty(t, cfg.CronFiles())
}

// TestIncludeCron_OverlapWithIncludeIsHardError: the two readings of one file
// produce different task sets, and silently picking one would make the config
// mean something nobody wrote.
func TestIncludeCron_OverlapWithIncludeIsHardError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/*"]
include_cron = ["conf.d/*"]
`,
		"conf.d/a.toml": "[tasks.a]\nrun = \"echo a\"\n",
	})

	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both daemon.include and daemon.include_cron")
}

// TestIncludeCron_WorldWritableFileRefused: include_cron makes every matched path
// an author of daemon-privileged shell. A group- or world-writable crontab is
// then a privilege escalation for anyone who can write it.
func TestIncludeCron_WorldWritableFileRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the ownership half of the check is trivially satisfied as root")
	}
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/backup": "0 3 * * * /usr/local/bin/backup.sh\n",
	})
	require.NoError(t, os.Chmod(filepath.Join(dir, "crontabs", "backup"), 0o666))

	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writable by group or others")
}

// TestIncludeCron_WorldWritableDirRefused: a writable directory lets an attacker
// replace the file wholesale, which the file's own mode says nothing about.
func TestIncludeCron_WorldWritableDirRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the ownership half of the check is trivially satisfied as root")
	}
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/backup": "0 3 * * * /usr/local/bin/backup.sh\n",
	})
	require.NoError(t, os.Chmod(filepath.Join(dir, "crontabs"), 0o777))

	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writable by group or others")
}

// TestIncludeCron_NoExpansionOfCronText pairs with the importer's ${...}
// escaping: crond performs no substitution, so neither may we. Without the
// escape this load fails outright ("environment variable DB is not set"), which
// would take every other job in the crontab down with it.
func TestIncludeCron_NoExpansionOfCronText(t *testing.T) {
	t.Setenv("RUNWISP_TEST_CRON_CANARY", "LEAKED")
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/dump": "CANARY=${RUNWISP_TEST_CRON_CANARY}\n" +
			"\n# nightly ${DB} dump\n" +
			"0 3 * * * /usr/local/bin/dump.sh ${DB}\n",
	})

	cfg := loadTree(t, dir)
	task := findTask(t, cfg, "dump")
	assert.Contains(t, task.Description, "${DB}")
	assert.Equal(t, "${RUNWISP_TEST_CRON_CANARY}", task.Env["CANARY"])
	assert.Contains(t, task.Run, "${DB}", "run reaches the shell verbatim")
	assert.NotContains(t, strings.Join([]string{task.Description, task.Env["CANARY"], task.Run}, "\n"), "LEAKED")
}

// TestIncludeCron_StaleAfterEdit: `crontab -e` must make `runwisp status` say
// "config changed on disk", which is what tells the operator to reload.
func TestIncludeCron_StaleAfterEdit(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/backup": "0 3 * * * /usr/local/bin/backup.sh\n",
	})
	root := filepath.Join(dir, "runwisp.toml")
	cfg := loadTree(t, dir)

	snap := NewSnapshot(root, cfg, time.Now())
	require.False(t, snap.Stale())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "crontabs", "backup"),
		[]byte("0 5 * * * /usr/local/bin/backup.sh\n"), 0o600))
	assert.True(t, snap.Stale(), "an edited crontab is a changed config")
}

// TestIncludeCron_InIncludedFileIsHardError: include_cron lives in [daemon],
// which may only appear in the root config, so this is really a check that the
// existing singleton rule covers the new key.
func TestIncludeCron_InIncludedFileIsHardError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/*.toml"]
`,
		"conf.d/a.toml": `
[daemon]
include_cron = ["/etc/cron.d/*"]

[tasks.a]
run = "echo a"
`,
	})

	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "[daemon]")
}

// TestIncludeCron_SystemCrontabTakesItsUserColumn: a /etc/cron.d-shaped file
// carries a user column, and honoring it is privilege-*reducing* — the job runs
// as the account the crontab names rather than as the daemon.
func TestIncludeCron_SystemCrontabTakesItsUserColumn(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["cron.d/*"]
`,
		// A path containing cron.d is what makes this the system format — the sixth
		// field is a username, not the first word of the command.
		"cron.d/backup": "0 3 * * * postgres /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	task := findTask(t, cfg, "backup")
	assert.Equal(t, "postgres", task.RunUser)
	assert.Equal(t, "/usr/local/bin/backup.sh", task.Run, "the user column is not part of the command")
	assert.Equal(t, "~", task.WorkingDir, "crond runs a system job in its own user's home")
}

// TestIncludeCron_PerUserFileRunsAsTheDaemon: nothing infers a run identity from
// a path. A per-user-shaped file's jobs run as the daemon's own account, which is
// documented rather than guessed — a filename deciding a uid would be a
// privilege escalation dressed as a convenience.
func TestIncludeCron_PerUserFileRunsAsTheDaemon(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["spool/*"]
`,
		"spool/alice": "0 3 * * * /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	task := findTask(t, cfg, "backup")
	assert.Empty(t, task.RunUser, "no identity is inferred from the filename")

	// working_dir still resolves to a home, but the daemon's own — crond runs a
	// per-user job in that user's home, and here that user *is* the daemon.
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, home, task.WorkingDir)
}

func TestCronNameSuffix(t *testing.T) {
	cases := map[string]string{
		"/etc/cron.d/backup":     "backup",
		"/etc/cron.d/my_backups": "my_backups",
		"/var/spool/cron/alice":  "alice",
		"/etc/cron.d/a.tab":      "a",
	}
	for path, want := range cases {
		assert.Equal(t, want, cronNameSuffix(path), path)
	}
}

// TestIncludeCron_BareWordCommandStillRuns is the regression for the sniff that
// asks whether a line's sixth field looks like a username. On a per-user file it
// fires on any bare-word command — `echo ticked`, `psql -c …`, `borg create …` —
// and for a live source a suspect row is dropped, not annotated. So with the sniff
// on, ordinary crontabs silently ran nothing at all.
func TestIncludeCron_BareWordCommandStillRuns(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/jobs": "* * * * * echo ticked\n",
	})

	cfg := loadTree(t, dir)
	task := findTask(t, cfg, "echo")
	assert.Equal(t, "echo ticked", task.Run, "the whole command, with no field read as a username")
	assert.Empty(t, task.RunUser)
	assert.Empty(t, cfg.CronFindings, "and nothing to warn about")
}

// TestCrondStillRunningWarning is the guard on the most likely way an include_cron
// setup goes wrong, and the one that goes wrong silently: RunWisp and cron both
// scheduling /etc/crontab means every job fires twice, which looks like nothing at
// all until a non-idempotent one does it.
func TestCrondStillRunningWarning(t *testing.T) {
	dir := t.TempDir()
	livePid := filepath.Join(dir, "crond.pid")
	require.NoError(t, os.WriteFile(livePid, []byte("1234\n"), 0o600))
	missingPid := filepath.Join(dir, "nope.pid")

	t.Run("system crontab plus a live pidfile warns", func(t *testing.T) {
		got := crondStillRunningWarning([]string{"/etc/cron.d/backup"}, []string{missingPid, livePid})
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "/etc/cron.d/backup")
		assert.Contains(t, got[0], "twice")
	})

	t.Run("no pidfile, no warning", func(t *testing.T) {
		assert.Empty(t, crondStillRunningWarning([]string{"/etc/crontab"}, []string{missingPid}))
	})

	// A per-user file the operator handed us is not something cron reads on its
	// own, so warning about it would be crying wolf on every single boot.
	t.Run("per-user source alone does not warn", func(t *testing.T) {
		assert.Empty(t, crondStillRunningWarning([]string{"/home/me/crontab"}, []string{livePid}))
	})
}
