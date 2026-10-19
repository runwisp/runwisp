// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cronPatterns is what a root-euid scan would offer, shortened to two entries so
// the assertions stay readable.
var cronPatterns = []string{"/etc/crontab", "/etc/cron.d/*"}

func TestEnsureCronInclude_AppendsWhenNoDaemonTable(t *testing.T) {
	root := "[tasks.web]\nrun = \"echo hi\"\n"
	out, _, err := EnsureCronInclude([]byte(root), cronPatterns)
	require.NoError(t, err)
	s := string(out)
	assert.Contains(t, s, "[tasks.web]")
	assert.Contains(t, s, "[daemon]")
	assert.Contains(t, s, "include_cron = [\n  \"/etc/crontab\",\n  \"/etc/cron.d/*\",\n]\n")
}

func TestEnsureCronInclude_InsertsAfterExistingDaemonHeader(t *testing.T) {
	root := "[daemon]\nshutdown_timeout = \"10s\"\n\n[tasks.web]\nrun = \"echo hi\"\n"
	out, _, err := EnsureCronInclude([]byte(root), cronPatterns)
	require.NoError(t, err)
	s := string(out)
	// The key lands right after the [daemon] header, before the existing key, and
	// the rest of the file is untouched.
	daemonIdx := strings.Index(s, "[daemon]\n")
	includeIdx := strings.Index(s, "include_cron = [")
	shutdownIdx := strings.Index(s, "shutdown_timeout")
	require.Greater(t, includeIdx, daemonIdx)
	require.Greater(t, shutdownIdx, includeIdx)
	assert.Contains(t, s, "shutdown_timeout = \"10s\"")
	assert.Contains(t, s, "[tasks.web]\nrun = \"echo hi\"\n")
}

// TestEnsureCronInclude_RefusesWhenAlreadySet is the policy difference from
// EnsureStagingInclude: there is no "already covered" success case, because
// whether a config actually reads crontabs is the loader's answer, not this
// package's. Anything already declared is the operator's list.
func TestEnsureCronInclude_RefusesWhenAlreadySet(t *testing.T) {
	for _, existing := range []string{
		`include_cron = ["/etc/crontab"]`,
		`include_cron = ["/var/spool/cron/crontabs/*"]`,
		`include_cron = []`, // "read no crontabs" is an answer too
	} {
		root := "[daemon]\n" + existing + "\n"
		_, _, err := EnsureCronInclude([]byte(root), cronPatterns)
		assert.ErrorIs(t, err, ErrCronIncludeAlreadySet, existing)
	}
}

// TestEnsureCronInclude_ExtendsItsOwnScaffold is the re-run take-over on a box
// an older RunWisp scaffolded: its include_cron lacks /etc/anacrontab, and since
// the array is still exactly what RunWisp wrote, the missing pattern is added
// rather than blocking the take-over on a paste. The same list reformatted, or
// with a path RunWisp never offers, is the operator's.
func TestEnsureCronInclude_ExtendsItsOwnScaffold(t *testing.T) {
	now := []string{"/etc/crontab", "/etc/cron.d/*", "/etc/anacrontab"}
	root := config.CronStarterConfig(cronPatterns)

	out, added, err := EnsureCronInclude([]byte(root), now)
	require.NoError(t, err)
	assert.Equal(t, []string{"/etc/anacrontab"}, added)
	assert.Equal(t, config.CronStarterConfig(now), string(out))

	out, added, err = EnsureCronInclude(out, now)
	require.NoError(t, err)
	assert.Empty(t, added, "a second run has nothing to add")
	assert.Equal(t, config.CronStarterConfig(now), string(out))

	for _, theirs := range []string{
		strings.Replace(root, `  "/etc/cron.d/*",`, `  "/etc/cron.d/*", # mine`, 1),
		config.CronStarterConfig([]string{"/etc/crontab", "/opt/jobs.cron"}),
	} {
		_, _, err := EnsureCronInclude([]byte(theirs), append(now, "/opt/x"))
		assert.ErrorIs(t, err, ErrCronIncludeAlreadySet, theirs)
	}
}

func TestEnsureCronInclude_HandlesHeaderWithTrailingComment(t *testing.T) {
	root := "[daemon] # my daemon settings\nshutdown_timeout = \"10s\"\n"
	out, _, err := EnsureCronInclude([]byte(root), cronPatterns)
	require.NoError(t, err)
	assert.Contains(t, string(out), "include_cron = [")
}

func TestEnsureCronInclude_AppendsWhenNoTrailingNewline(t *testing.T) {
	root := "[tasks.web]\nrun = \"echo hi\"" // no trailing newline
	out, _, err := EnsureCronInclude([]byte(root), cronPatterns)
	require.NoError(t, err)
	assert.NotContains(t, string(out), `hi"[daemon]`, "insertion must not fuse onto the last line")
	assert.Contains(t, string(out), "[daemon]")
}

// TestEnsureCronInclude_IgnoresHeaderInsideMultilineString guards the sharp edge
// of editing TOML as text: a `run` body can contain a line that reads exactly
// like a table header. Inserting include_cron there would bury it inside a shell
// script — a config that still parses but reads no crontabs at all, which is
// precisely the state the cutover is trying to leave behind.
func TestEnsureCronInclude_IgnoresHeaderInsideMultilineString(t *testing.T) {
	root := "[tasks.deploy]\nrun = \"\"\"\n" +
		"cat <<EOF > /tmp/other.toml\n" +
		"[daemon]\n" +
		"shutdown_timeout = \"1s\"\n" +
		"EOF\n" +
		"\"\"\"\n\n" +
		"[daemon]\nshutdown_timeout = \"10s\"\n"

	out, _, err := EnsureCronInclude([]byte(root), cronPatterns)
	require.NoError(t, err)
	s := string(out)

	realDaemon := strings.LastIndex(s, "[daemon]\n")
	include := strings.Index(s, "include_cron = [")
	assert.Greater(t, include, realDaemon, "include_cron must follow the real [daemon] table:\n%s", s)
	assert.Contains(t, s, "cat <<EOF > /tmp/other.toml\n[daemon]\nshutdown_timeout = \"1s\"\nEOF\n",
		"the run body must come through untouched")
}

// TestEnsureCronInclude_WiredConfigLoadsAndReadsTheCrontab proves the surgically
// wired root actually loads and picks the crontab up as a live task — the whole
// point of the edit. It uses a crontab-format file outside cron's own paths, so
// the loader reads it without a hold being derived.
func TestEnsureCronInclude_WiredConfigLoadsAndReadsTheCrontab(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": "[tasks.native]\nrun = \"echo native\"\n",
		"jobs.cron":    "17 3 * * * /usr/bin/backup\n",
	})
	rootPath := filepath.Join(dir, "runwisp.toml")

	orig, err := os.ReadFile(rootPath)
	require.NoError(t, err)
	wired, _, err := EnsureCronInclude(orig, []string{"jobs.cron"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(rootPath, wired, 0o600))

	cfg, err := config.Load(rootPath)
	require.NoError(t, err)
	require.Len(t, cfg.CronFiles(), 1, "the wired config must read the crontab")

	names := taskNames(cfg)
	assert.Contains(t, names, "native")
	require.Len(t, names, 2, "the crontab's one job must load alongside the native task, got %v", names)
	for _, n := range names {
		if n == "native" {
			continue
		}
		assert.Equal(t, model.SourceCron, findTask(t, cfg, n).Source)
	}
}

func TestEnsureCronInclude_InvalidTOMLErrors(t *testing.T) {
	_, _, err := EnsureCronInclude([]byte("[daemon\nbroken"), cronPatterns)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrCronIncludeAlreadySet))
}

func TestEnsureCronInclude_RefusesEmptyPatterns(t *testing.T) {
	_, _, err := EnsureCronInclude([]byte("[daemon]\n"), nil)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrCronIncludeAlreadySet))
}
