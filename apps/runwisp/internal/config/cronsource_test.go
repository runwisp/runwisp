// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strconv"
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

// stubCronUsers replaces the machine's account database with the given names for
// the duration of one test, so a system crontab naming `postgres` or `deploy`
// loads the same on a laptop as on the box it was written for.
func stubCronUsers(t *testing.T, names ...string) {
	t.Helper()
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	prev := cronUserExists
	cronUserExists = func(name string) bool { return set[name] }
	t.Cleanup(func() { cronUserExists = prev })
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

// TestIncludeCron_GlobIgnoresWhatCrondIgnores is the wrong-direction guard: every
// other skip in this file stops something from running, but reading a file crond
// passes over *starts* something running — jobs the operator believes are disabled,
// or a superseded copy of jobs that also run from the live file.
func TestIncludeCron_GlobIgnoresWhatCrondIgnores(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/live":          "0 3 * * * /usr/local/bin/live.sh\n",
		"crontabs/old.dpkg-old":  "0 4 * * * /usr/local/bin/superseded.sh\n",
		"crontabs/held.disabled": "0 5 * * * /usr/local/bin/switched-off.sh\n",
		"crontabs/README":        "notes for whoever maintains this directory\n",
		"crontabs/nested/deep":   "0 6 * * * /usr/local/bin/nested.sh\n",
	})

	cfg := loadTree(t, dir)
	assert.Equal(t, []string{"live"}, taskNames(cfg),
		"only files crond would read may contribute tasks")

	// README is dotless, so crond reads it too and complains about its contents —
	// which is exactly what happens here: it is opened as a crontab and its prose
	// becomes an ordinary unparseable-job finding. Parity, not an oversight.
	assert.Equal(t, []string{
		filepath.Join(dir, "crontabs", "README"),
		filepath.Join(dir, "crontabs", "live"),
	}, cfg.CronFiles())

	// The name rule accounts for the two dotted files; the directory for the nested
	// one.
	byBase := map[string]CronFinding{}
	for _, f := range cfg.CronFindings {
		byBase[filepath.Base(f.File)] = f
	}
	for base, want := range map[string]string{
		"old.dpkg-old":  "crond ignores this name",
		"held.disabled": "crond ignores this name",
		"nested":        "it is a directory",
	} {
		finding, ok := byBase[base]
		require.True(t, ok, "no finding for %s — it was passed over silently", base)
		assert.Contains(t, finding.Reason, want)
		assert.False(t, finding.Skipped,
			"nothing stopped running: crond wasn't running this either")
	}

	// The operator who dropped in `held.disabled` and can't find its tasks has to be
	// able to learn that the name is the reason.
	warnings := strings.Join(Warnings(cfg), "\n")
	assert.Contains(t, warnings, "held.disabled")
}

// TestIncludeCron_MailtoIsReported is the quietest regression include_cron could
// have shipped: a crontab that had been mailing its output for years goes silent on
// the switch, and every job still reports success. The note existed the whole time —
// it just belonged to the file rather than to a job, and only the per-job notes were
// being read.
func TestIncludeCron_MailtoIsReported(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/nightly": "MAILTO=ops@example.com\n0 3 * * * /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	assert.Equal(t, []string{"backup"}, taskNames(cfg), "the job still runs; only the mail is missing")

	require.Len(t, cfg.CronFindings, 1)
	finding := cfg.CronFindings[0]
	assert.False(t, finding.Skipped, "nothing stopped running")
	assert.Equal(t, filepath.Join(dir, "crontabs", "nightly"), finding.File)
	assert.Zero(t, finding.Line, "a file-level finding has no line")
	assert.Contains(t, finding.Reason, "MAILTO")

	warnings := strings.Join(Warnings(cfg), "\n")
	assert.Contains(t, warnings, "MAILTO")
}

// TestIncludeCron_MailtoIsSilentOnceMailWorks: the finding names an unmet need, so
// it has to stop once the need is met. A warning that keeps firing after it's been
// dealt with is how an operator learns to stop reading warnings.
func TestIncludeCron_MailtoIsSilentOnceMailWorks(t *testing.T) {
	for _, notifierType := range []string{"sendmail", "smtp"} {
		t.Run(notifierType, func(t *testing.T) {
			extra := ""
			if notifierType == "smtp" {
				extra = "host = \"mail.example.com\"\nport = 587\n"
			}
			dir := writeFileTree(t, map[string]string{
				"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]

[[notifier]]
id = "mail"
type = "` + notifierType + `"
from = "runwisp@example.com"
to = ["ops@example.com"]
` + extra,
				"crontabs/nightly": "MAILTO=ops@example.com\n0 3 * * * /usr/local/bin/backup.sh\n",
			})

			cfg := loadTree(t, dir)
			assert.Empty(t, cfg.CronFindings,
				"mail is configured, so the crontab's MAILTO is no longer an open question")
		})
	}
}

// TestIncludeCron_NonAbsoluteShellIsReported: the other file-level note. crond's
// SHELL=bash is not honoured (RunWisp needs an absolute path), so a bash-ism in a
// job fails — visibly, in the run's output, but the operator should not have to
// diagnose it from there.
func TestIncludeCron_NonAbsoluteShellIsReported(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
		"crontabs/nightly": "SHELL=bash\n0 3 * * * /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	require.Len(t, cfg.CronFindings, 1)
	assert.Contains(t, cfg.CronFindings[0].Reason, "SHELL")
	assert.False(t, cfg.CronFindings[0].Skipped)
}

// TestIncludeCron_LiterallyNamedFileIsReadWhateverItsName: the name rule filters a
// glob's hits, never a path the operator typed. Declining to read a file that was
// asked for by name would be the daemon overruling an explicit instruction.
func TestIncludeCron_LiterallyNamedFileIsReadWhateverItsName(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/backup.cron"]
`,
		"crontabs/backup.cron": "0 3 * * * /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	assert.Equal(t, []string{"backup"}, taskNames(cfg))
	assert.Empty(t, cfg.CronFindings)
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

// TestIncludeCron_OneBadFileDoesNotTakeDownTheRest is the fail-open guard.
// Before this, one unreadable crontab rejected the whole config load — under
// Restart=on-failure that is a five-second restart loop on a box that now
// runs nothing at all, cron included, if an earlier boot already masked it.
// The trigger is mundane (an account that got userdel'd, one file at the
// wrong mode) and shouldn't be able to take every other task down with it.
func TestIncludeCron_OneBadFileDoesNotTakeDownTheRest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file")
	}
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]

[tasks.native]
run = "echo hi"
`,
		"crontabs/good": "0 3 * * * /usr/local/bin/good.sh\n",
	})
	bad := filepath.Join(dir, "crontabs", "bad")
	require.NoError(t, os.WriteFile(bad, []byte("0 4 * * * /usr/local/bin/bad.sh\n"), 0o000))

	cfg := loadTree(t, dir)
	assert.ElementsMatch(t, []string{"native", "good"}, taskNames(cfg),
		"the good source and the native task both still load")

	var badFinding *CronFinding
	for i, f := range cfg.CronFindings {
		if f.File == bad {
			badFinding = &cfg.CronFindings[i]
		}
	}
	require.NotNil(t, badFinding, "the unreadable file is reported, not silently dropped")
	assert.True(t, badFinding.Skipped)
}

// TestIncludeCron_EveryMatchedSourceFailingIsStillAHardError: unlike one bad
// file among several good ones, nothing at all parsing suggests the
// include_cron pattern itself is wrong rather than one crontab having gone
// bad, and that is worth surfacing as loudly as a config that fails to load.
func TestIncludeCron_EveryMatchedSourceFailingIsStillAHardError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file")
	}
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]
`,
	})
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "crontabs"), 0o755))
	for _, name := range []string{"one", "two"} {
		path := filepath.Join(dir, "crontabs", name)
		require.NoError(t, os.WriteFile(path, []byte("0 3 * * * /bin/true\n"), 0o000))
	}

	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the 2 matched cron source")
}

// TestCrondGlobEligibleName is the guard on the divergence between two rules
// for "is this a crontab crond would read": the strict run-parts rule for a
// system crontab directory, and the much looser account-name rule for a
// per-user spool. A prior version applied the strict rule everywhere,
// including to spool globs, and silently dropped a spool crontab for any
// account whose name has a dot or a trailing $ — both legal, common
// characters in a real account name that importer.IsPlausibleAccountName
// already accepts.
func TestCrondGlobEligibleName(t *testing.T) {
	t.Run("a system crontab directory uses the strict run-parts rule", func(t *testing.T) {
		assert.True(t, crondGlobEligibleName("/etc/cron.d/backup"))
		assert.False(t, crondGlobEligibleName("/etc/cron.d/backup.dpkg-old"))
	})
	t.Run("a spool directory allows a dotted or $-suffixed account name", func(t *testing.T) {
		assert.True(t, crondGlobEligibleName("/var/spool/cron/crontabs/john.doe"))
		assert.True(t, crondGlobEligibleName("/var/spool/cron/crontabs/svc$"))
		assert.True(t, crondGlobEligibleName("/var/spool/cron/deploy"))
	})
	t.Run("a spool directory still excludes crontab -e's tmp file", func(t *testing.T) {
		assert.False(t, crondGlobEligibleName("/var/spool/cron/crontabs/tmp.12345"),
			"vixie cron writes tmp.<pid> here before its atomic rename; it is never a real crontab")
	})
	t.Run("a directory that merely happens to be named crontabs is not a spool", func(t *testing.T) {
		assert.False(t, crondGlobEligibleName("/home/alice/crontabs/john.doe"),
			"only the two canonical spool paths get the looser rule")
		assert.True(t, crondGlobEligibleName("/home/alice/crontabs/john-doe"))
	})
}

// TestUnlistableDirFinding is the guard on the other direction of the same
// bug family: filepath.Glob ignores a readdir permission error by contract
// and returns zero matches, which is indistinguishable from a directory that
// is genuinely empty. /var/spool/cron/crontabs ships 1730 root:crontab on a
// real box, so a non-root daemon following the documented include_cron
// example would boot clean, schedule nothing, and say nothing at all.
func TestUnlistableDirFinding(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can list any directory")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "crontabs")
	require.NoError(t, os.Mkdir(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o300))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	got := unlistableDirFinding(filepath.Join(sub, "*"))
	require.NotNil(t, got)
	assert.Equal(t, sub, got.Path)
	assert.True(t, got.Skipped, "crond, running as root, could read this directory even if RunWisp can't")
	assert.Contains(t, got.Reason, "cannot list")
}

func TestUnlistableDirFinding_ReadableDirIsFine(t *testing.T) {
	dir := t.TempDir()
	assert.Nil(t, unlistableDirFinding(filepath.Join(dir, "*")))
}

func TestUnlistableDirFinding_MissingDirIsFine(t *testing.T) {
	assert.Nil(t, unlistableDirFinding(filepath.Join(t.TempDir(), "nope", "*")),
		"a directory that doesn't exist yet is a normal machine, not a finding")
}

// TestIncludeCron_UnlistableDirIsReportedNotSilent is the end-to-end version
// of TestUnlistableDirFinding: the finding has to actually reach
// Config.CronFindings through resolveCronIncludes, and the rest of the config
// must still load around it.
func TestIncludeCron_UnlistableDirIsReportedNotSilent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can list any directory")
	}
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["crontabs/*"]

[tasks.native]
run = "echo hi"
`,
	})
	sub := filepath.Join(dir, "crontabs")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "alice"), []byte("* * * * * echo hi\n"), 0o600))
	require.NoError(t, os.Chmod(sub, 0o300))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	cfg := loadTree(t, dir)
	assert.Equal(t, []string{"native"}, taskNames(cfg), "the rest of the config still loads")
	require.Len(t, cfg.CronFindings, 1)
	assert.True(t, cfg.CronFindings[0].Skipped)
	assert.Contains(t, cfg.CronFindings[0].Reason, "cannot list")
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
//
// include_cron names the file literally rather than globbing it, because a glob
// can no longer reach a `.toml` at all — crond's naming rule filters dotted names
// out of a glob's hits. A literal path is the operator overriding that, and so the
// one route by which the same file can still arrive down both readings.
func TestIncludeCron_OverlapWithIncludeIsHardError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/*"]
include_cron = ["conf.d/a.toml"]
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
	assert.Contains(t, err.Error(), "world-writable")
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
	assert.Contains(t, err.Error(), "world-writable")
}

// TestCronTrust_StickyGroupWritableDirAccepted is what made per-user crontabs
// readable at all. A cron spool is 1730 root:crontab on every Debian box — group
// writable so the setgid crontab(1) can drop a file in, sticky so a group member
// still cannot touch anyone else's. Refusing group-writable outright refused the
// exact configuration the OS ships, which is why the spool was a hard load error
// rather than merely unmapped.
func TestCronTrust_StickyGroupWritableDirAccepted(t *testing.T) {
	dir := t.TempDir()
	spool := filepath.Join(dir, "crontabs")
	require.NoError(t, os.Mkdir(spool, 0o700))
	path := filepath.Join(spool, "alice")
	require.NoError(t, os.WriteFile(path, []byte("0 3 * * * /bin/true\n"), 0o600))
	require.NoError(t, os.Chmod(spool, os.ModeSticky|0o730))

	require.NoError(t, assertCronFileTrusted(path, ""),
		"a sticky group-writable directory is what a real cron spool looks like")

	// Without the sticky bit the same mode is a genuine hole: a group member can
	// rename the file out of the way and put their own there.
	require.NoError(t, os.Chmod(spool, 0o730))
	err := assertCronFileTrusted(path, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writable by its group")
}

// TestCronTrust_GroupWritableFileRefusedEvenIfSticky: the carve-out is for the
// directory only. A sticky bit on a regular file means something else entirely and
// buys no protection against whoever can write it.
func TestCronTrust_GroupWritableFileRefusedEvenIfSticky(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup")
	require.NoError(t, os.WriteFile(path, []byte("0 3 * * * /bin/true\n"), 0o600))
	require.NoError(t, os.Chmod(path, os.ModeSticky|0o660))

	err := assertCronFileTrusted(path, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writable by its group")
}

// TestCronTrust_UnresolvableRunAsRefused: the run-as account is what corroborates
// the ownership of a spool file, so an account this machine can't resolve leaves
// the one check standing between that file and daemon-privileged execution
// unmakeable. Refuse while there's still a place to say why.
func TestCronTrust_UnresolvableRunAsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nobody-by-that-name")
	require.NoError(t, os.WriteFile(path, []byte("0 3 * * * /bin/true\n"), 0o600))

	err := assertCronFileTrusted(path, "runwisp-no-such-account-9f3a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve that account")
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
	stubCronUsers(t, "postgres")
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

// TestIncludeCron_SystemLineMissingItsUserColumnIsNotScheduled is the bug this
// check exists for: a /etc/cron.d line written without its user column reads as
// user `echo` running `"…"`, and `echo` is a perfectly well-shaped login name, so
// the shape sniff alone let it through — the daemon scheduled a task nobody wrote,
// which then failed as `user "echo" not found` once a minute, forever.
//
// crond declines the line because the sixth field is no account. So does RunWisp.
func TestIncludeCron_SystemLineMissingItsUserColumnIsNotScheduled(t *testing.T) {
	stubCronUsers(t, "root", "deploy")
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["cron.d/*"]
`,
		"cron.d/broken": "* * * * * echo \"no user column, should never run\"\n" +
			"0 4 * * * deploy /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	assert.Equal(t, []string{"backup"}, taskNames(cfg),
		"the malformed line must not become a task, and must not take the good line with it")

	require.Len(t, cfg.CronFindings, 1)
	f := cfg.CronFindings[0]
	assert.True(t, f.Skipped, "a job that isn't running has to say so")
	assert.Equal(t, 1, f.Line, "the operator needs file:line, not a derived task name")
	assert.Contains(t, f.Reason, "echo")

	// And it reaches the operator: this is the warning list boot and `runwisp
	// validate` both print.
	warnings := strings.Join(cronSourceWarnings(cfg), "\n")
	assert.Contains(t, warnings, "cron source: skipped")
	assert.Contains(t, warnings, "cron.d/broken")
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

// stubCronServiceProbe replaces the systemctl-backed liveness probe for the
// duration of one test, so a test can assert both branches (active/enabled
// via the init system, and the pid-file fallback when it's unavailable)
// without a real systemd on the machine running the suite.
func stubCronServiceProbe(t *testing.T, active, enabled, ok bool) {
	t.Helper()
	prev := cronServiceProbe
	cronServiceProbe = func() (bool, bool, bool) { return active, enabled, ok }
	t.Cleanup(func() { cronServiceProbe = prev })
}

// TestCrondStillRunningWarning is the guard on the most likely way an include_cron
// setup goes wrong, and the one that goes wrong silently: RunWisp and cron both
// scheduling the same crontabs means every job fires twice, which looks like
// nothing at all until a non-idempotent one does it.
func TestCrondStillRunningWarning(t *testing.T) {
	dir := t.TempDir()
	livePid := filepath.Join(dir, "crond.pid")
	require.NoError(t, os.WriteFile(livePid, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600))
	deadPid := filepath.Join(dir, "dead.pid")
	require.NoError(t, os.WriteFile(deadPid, []byte("999999999\n"), 0o600))
	missingPid := filepath.Join(dir, "nope.pid")

	t.Run("systemctl reports active warns", func(t *testing.T) {
		stubCronServiceProbe(t, true, false, true)
		got := crondStillRunningWarning([]string{"/etc/cron.d/backup"}, []string{missingPid})
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "/etc/cron.d/backup")
		assert.Contains(t, got[0], "twice")
	})

	t.Run("systemctl reports enabled but not active still warns", func(t *testing.T) {
		stubCronServiceProbe(t, false, true, true)
		got := crondStillRunningWarning([]string{"/etc/crontab"}, []string{missingPid})
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "next boot")
	})

	t.Run("systemctl reports neither active nor enabled, no warning", func(t *testing.T) {
		stubCronServiceProbe(t, false, false, true)
		assert.Empty(t, crondStillRunningWarning([]string{"/etc/crontab"}, []string{livePid}),
			"the init system is the authority when it can answer at all")
	})

	// A spool-only include used to never warn at all, because the old check
	// filtered cronFiles down to system crontab paths before looking at any
	// pidfile. A live crond reads /var/spool/cron just as much as
	// /etc/cron.d, so it has to warn here too.
	t.Run("systemctl unavailable, live pidfile warns even for a spool-only include", func(t *testing.T) {
		stubCronServiceProbe(t, false, false, false)
		got := crondStillRunningWarning([]string{"/var/spool/cron/crontabs/alice"}, []string{missingPid, livePid})
		require.Len(t, got, 1)
		assert.Contains(t, got[0], "crontabs/alice")
	})

	// The old version only checked that the pidfile existed, not that the pid
	// inside it was still a live process — so a crond that crashed without
	// cleaning up, or a stale file baked into an image, warned forever.
	t.Run("systemctl unavailable, stale pidfile does not warn", func(t *testing.T) {
		stubCronServiceProbe(t, false, false, false)
		assert.Empty(t, crondStillRunningWarning([]string{"/etc/crontab"}, []string{deadPid}))
	})

	t.Run("no cron files, no warning even if crond is live", func(t *testing.T) {
		stubCronServiceProbe(t, true, true, true)
		assert.Empty(t, crondStillRunningWarning(nil, []string{livePid}))
	})
}

// TestRunUserFindings_NonRootDaemonCannotBecomeCronsUser is the gap
// evaluateCronTakeover's refusal #4 exists to close: a non-root daemon reads
// /etc/crontab's or cron.d's root-column jobs cleanly today — no
// CronFinding, nothing — and every one of them fails at exec time because
// switching OS user needs a capability only root has.
func TestRunUserFindings_NonRootDaemonCannotBecomeCronsUser(t *testing.T) {
	stubCronUsers(t, "root")
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include_cron = ["cron.d/*"]
`,
		"cron.d/backup": "0 3 * * * root /usr/local/bin/backup.sh\n",
	})

	cfg := loadTree(t, dir)
	task := findTask(t, cfg, "backup")
	require.Equal(t, "root", task.RunUser)
	require.Empty(t, cfg.CronFindings, "it loads clean today — that's exactly the gap this closes")

	assert.Equal(t, []string{"backup"}, RunUserFindings(cfg, 1000),
		"a non-root daemon cannot switch to root at run time")
	assert.Empty(t, RunUserFindings(cfg, 0), "root can become anyone")
}

// TestRunUserFindings_IgnoresNativeTasks scopes the check to cron-sourced
// tasks only. A hand-written `user =` on a native TOML task is the
// operator's own explicit choice, already a general (not cron-specific)
// question the take-over gate has no business blocking on.
func TestRunUserFindings_IgnoresNativeTasks(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[tasks.native]
run = "true"
user = "root"
`,
	})
	cfg := loadTree(t, dir)
	assert.Empty(t, RunUserFindings(cfg, 1000))
}
