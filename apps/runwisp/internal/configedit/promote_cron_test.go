// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cronLayout stages a root config pointing at one crontab and returns the layout.
func cronLayout(t *testing.T, root, crontab string) Layout {
	t.Helper()
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":    root,
		"crontabs/mycron": crontab,
	})
	return NewLayout(filepath.Join(dir, "runwisp.toml"))
}

const cronRoot = `[daemon]
include_cron = ["crontabs/*"]

# my own task, hand-authored
[tasks.mine]
cron = "@daily"
run = "echo mine"
`

// cronPath resolves the crontab a cronLayout points at.
func cronPath(layout Layout) string {
	return filepath.Join(filepath.Dir(layout.RootPath), "crontabs", "mycron")
}

// TestPromote_CronSourcedTaskIsARealMove is the core regression: promoting a
// live cron-sourced task is not just a copy into root — the exact source line
// comes out of the crontab in the same transaction, so nothing is left for a
// still-live system cron daemon to fire a second time. Every other byte in the
// crontab — a preceding comment, an unrelated job, its own comment, the file's
// mode — survives untouched.
func TestPromote_CronSourcedTaskIsARealMove(t *testing.T) {
	crontab := "# an unrelated job, not being promoted\n" +
		"*/10 * * * * /usr/local/bin/other.sh\n" +
		"# nightly dump\n" +
		"0 3 * * * /usr/local/bin/dump.sh --full\n"
	layout := cronLayout(t, cronRoot, crontab)
	path := cronPath(layout)
	require.NoError(t, os.Chmod(path, 0o640))

	cfg := loadLayout(t, layout)
	require.Equal(t, model.SourceCron, findTask(t, cfg, "dump").Source)
	require.ElementsMatch(t, []string{"dump", "other"}, PromotableNames(cfg))

	res, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.NoError(t, err)
	require.Len(t, res.Promoted, 1)
	assert.False(t, res.StagingRemoved, "there is no staging file in play")
	require.Len(t, res.CronRemovals, 1)
	assert.Equal(t, "dump", res.CronRemovals[0].Name)
	assert.Equal(t, path, res.CronRemovals[0].File)
	assert.Equal(t, 4, res.CronRemovals[0].Line)
	assert.Equal(t, "0 3 * * * /usr/local/bin/dump.sh --full", res.CronRemovals[0].Text)

	root := readFile(t, layout.RootPath)
	assert.Contains(t, root, "[tasks.dump]")
	assert.Contains(t, root, "/usr/local/bin/dump.sh --full")
	assert.Contains(t, root, "# my own task, hand-authored", "the operator's own file is untouched")

	after := readFile(t, cronPath(layout))
	assert.Equal(t,
		"# an unrelated job, not being promoted\n"+
			"*/10 * * * * /usr/local/bin/other.sh\n"+
			"# nightly dump\n",
		after, "only the promoted job's own line is gone; its lead comment and the unrelated job stay")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm(), "rewriting the crontab must not loosen its mode")
}

// TestPromote_CronSourcedTaskIsNativeAfterReloadWithNoDuplicate is the whole
// point: after the line is gone, a reload sees the task exactly once, sourced
// from root, with nothing left for a live cron daemon to double-fire. Before
// this fix, the crontab line survived promote and this reload would have
// depended on cron being fully retired to avoid double execution.
func TestPromote_CronSourcedTaskIsNativeAfterReloadWithNoDuplicate(t *testing.T) {
	layout := cronLayout(t, cronRoot, "0 3 * * * /usr/local/bin/dump.sh --full\n")
	cfg := loadLayout(t, layout)
	before := findTask(t, cfg, "dump")
	beforeRun, beforeCron := before.Run, before.Cron

	_, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.NoError(t, err)

	reloaded := loadLayout(t, layout)
	assert.ElementsMatch(t, []string{"mine", "dump"}, taskNames(reloaded),
		"dump must appear exactly once now that its crontab line is gone")

	after := findTask(t, reloaded, "dump")
	assert.Equal(t, model.SourceNative, after.Source)
	assert.Empty(t, after.SourceFile)
	assert.Equal(t, model.HeldByNothing, after.HeldBy,
		"a native task is never held, which is exactly why the crontab line has to be gone, not just copied around")
	assert.Equal(t, beforeRun, after.Run, "the promoted definition runs the same command")
	assert.Equal(t, beforeCron, after.Cron)

	assert.NotContains(t, readFile(t, cronPath(layout)), "dump.sh",
		"the crontab must no longer mention the promoted job at all")
}

// TestPromote_CronSourcedTaskRestampsRatherThanChanges is the guard on
// config.sameDefinition's provenance mask. A promote must read as a provenance-only
// restamp, because a Changed verdict would recycle a running service and re-anchor
// a cron task's schedule for a move that changed nothing about what runs.
func TestPromote_CronSourcedTaskRestampsRatherThanChanges(t *testing.T) {
	layout := cronLayout(t, cronRoot, "0 3 * * * /usr/local/bin/dump.sh --full\n")
	cfg := loadLayout(t, layout)

	_, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.NoError(t, err)
	reloaded := loadLayout(t, layout)

	diff := config.DiffTasks(tasksByName(cfg), tasksByName(reloaded))
	assert.Empty(t, diff.Added)
	assert.Empty(t, diff.Removed)
	assert.Empty(t, diff.Changed, "a promote changes no task definition")
	assert.Equal(t, []string{"dump"}, diff.Restamped)
}

// tasksByName indexes a config's tasks the way the reconciler's live registry does.
func tasksByName(cfg *config.Config) map[string]*model.Task {
	out := make(map[string]*model.Task, len(cfg.Tasks))
	for i := range cfg.Tasks {
		out[cfg.Tasks[i].Name] = &cfg.Tasks[i]
	}
	return out
}

// TestPromote_CronAndStagedTogether: one invocation, two provenances. The staged
// block is moved out of the staging file, the cron block copied in and its
// crontab line removed, and both end up in the root.
func TestPromote_CronAndStagedTogether(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `[daemon]
include = ["runwisp.d/*.toml"]
include_cron = ["crontabs/*"]
`,
		filepath.Join(config.ImportedStagingSubdir, config.ImportedStagingBase): "[tasks.staged_one]\nrun = \"echo staged\"\n",
		"crontabs/mycron": "0 3 * * * /usr/local/bin/dump.sh\n",
	})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	cfg := loadLayout(t, layout)
	require.ElementsMatch(t, []string{"staged_one", "dump"}, PromotableNames(cfg))

	res, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"staged_one", "dump"}})
	require.NoError(t, err)
	require.Len(t, res.Promoted, 2)
	assert.True(t, res.StagingRemoved, "the staging file held nothing else")
	require.Len(t, res.CronRemovals, 1)

	root := readFile(t, layout.RootPath)
	assert.Contains(t, root, "[tasks.staged_one]")
	assert.Contains(t, root, "[tasks.dump]")
	_, err = os.Stat(layout.StagingPath)
	assert.True(t, os.IsNotExist(err), "the emptied staging file is gone")
	assert.Empty(t, readFile(t, filepath.Join(dir, "crontabs", "mycron")),
		"the sole crontab line, now promoted, is gone")

	for _, name := range []string{"staged_one", "dump"} {
		assert.Equal(t, model.SourceNative, findTask(t, loadLayout(t, layout), name).Source, name)
	}
}

// TestPromote_CronRemovalGroupsMultipleLinesFromOneFile: --all across two
// cron-sourced tasks from the same crontab must delete both lines in one
// rewrite, leaving everything else — including a job neither name refers to —
// exactly as it was.
func TestPromote_CronRemovalGroupsMultipleLinesFromOneFile(t *testing.T) {
	crontab := "0 1 * * * /usr/local/bin/one.sh\n" +
		"# kept: nobody promoted this one\n" +
		"0 2 * * * /usr/local/bin/two.sh\n" +
		"0 3 * * * /usr/local/bin/three.sh\n"
	layout := cronLayout(t, cronRoot, crontab)
	cfg := loadLayout(t, layout)
	require.ElementsMatch(t, []string{"one", "two", "three"}, PromotableNames(cfg))

	res, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"one", "three"}})
	require.NoError(t, err)
	assert.Len(t, res.CronRemovals, 2)

	after := readFile(t, cronPath(layout))
	assert.Equal(t, "# kept: nobody promoted this one\n0 2 * * * /usr/local/bin/two.sh\n", after)
}

// TestPromote_CronSourceLineChangedSinceLoadRefuses is the safety rule: a
// crontab edited between load and promote must never have a line deleted on a
// stale line number's say-so alone. Nothing is written on either side.
func TestPromote_CronSourceLineChangedSinceLoadRefuses(t *testing.T) {
	layout := cronLayout(t, cronRoot, "0 3 * * * /usr/local/bin/dump.sh --full\n")
	cfg := loadLayout(t, layout)
	rootBefore := readFile(t, layout.RootPath)

	// The operator ran `crontab -e` (or hand-edited the file) after RunWisp
	// loaded but before promote ran.
	edited := "0 4 * * * /usr/local/bin/dump.sh --full --verbose\n"
	require.NoError(t, os.WriteFile(cronPath(layout), []byte(edited), 0o644))

	_, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.Error(t, err)
	var mismatch *CronSourceMismatchError
	require.ErrorAs(t, err, &mismatch)
	assert.Equal(t, "dump", mismatch.Name)

	assert.Equal(t, rootBefore, readFile(t, layout.RootPath), "nothing was written to the root config")
	assert.Equal(t, edited, readFile(t, cronPath(layout)), "the crontab is exactly as the operator left it")
}

// TestPromote_CronSourceLineShiftedRefuses covers the "stale line number,
// not just changed text" case explicitly: a line inserted above the promoted
// job's line shifts everything below it down by one, so the recorded line
// number now points at different text even though the job's own line is still
// in the file, just one line further down. Verifying line number and content
// together is what catches this; line number alone would not.
func TestPromote_CronSourceLineShiftedRefuses(t *testing.T) {
	layout := cronLayout(t, cronRoot, "0 3 * * * /usr/local/bin/dump.sh --full\n")
	cfg := loadLayout(t, layout)

	shifted := "*/5 * * * * /usr/local/bin/newcomer.sh\n" +
		"0 3 * * * /usr/local/bin/dump.sh --full\n"
	require.NoError(t, os.WriteFile(cronPath(layout), []byte(shifted), 0o644))

	_, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.Error(t, err)
	var mismatch *CronSourceMismatchError
	require.ErrorAs(t, err, &mismatch)

	assert.Equal(t, shifted, readFile(t, cronPath(layout)), "the crontab is untouched by the refused promote")
}

// TestPromote_CronSourceFileGoneRefuses covers the crontab having disappeared
// entirely (deleted, or the glob no longer matches it) between load and
// promote.
func TestPromote_CronSourceFileGoneRefuses(t *testing.T) {
	layout := cronLayout(t, cronRoot, "0 3 * * * /usr/local/bin/dump.sh --full\n")
	cfg := loadLayout(t, layout)
	rootBefore := readFile(t, layout.RootPath)

	require.NoError(t, os.Remove(cronPath(layout)))

	_, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.Error(t, err)
	var mismatch *CronSourceMismatchError
	require.ErrorAs(t, err, &mismatch)

	assert.Equal(t, rootBefore, readFile(t, layout.RootPath), "nothing was written to the root config")
	_, statErr := os.Stat(cronPath(layout))
	assert.True(t, os.IsNotExist(statErr), "promote must not recreate the missing crontab")
}

// TestPromote_CronSourceLineTruncatedRefuses covers a crontab that shrank —
// e.g. someone truncated it — so the recorded line number no longer exists at
// all, distinct from existing-but-changed.
func TestPromote_CronSourceLineTruncatedRefuses(t *testing.T) {
	crontab := "# a comment\n0 3 * * * /usr/local/bin/dump.sh --full\n"
	layout := cronLayout(t, cronRoot, crontab)
	cfg := loadLayout(t, layout)

	require.NoError(t, os.WriteFile(cronPath(layout), []byte("# a comment\n"), 0o644))

	_, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.Error(t, err)
	var mismatch *CronSourceMismatchError
	require.ErrorAs(t, err, &mismatch)
	assert.Contains(t, mismatch.Reason, "no longer exists",
		"the file has exactly one line now, and the trailing newline must not be counted as a phantom second one")
}

// TestPreviewCronRemovals_MatchesWhatPromoteWouldDo is the --dry-run
// guarantee: it must report the same file/line/text a real promote would
// remove, without writing anything, and it must refuse the same way on a
// stale line.
func TestPreviewCronRemovals_MatchesWhatPromoteWouldDo(t *testing.T) {
	layout := cronLayout(t, cronRoot, "0 3 * * * /usr/local/bin/dump.sh --full\n")
	cfg := loadLayout(t, layout)

	removals, err := PreviewCronRemovals([]string{"dump"}, cfg)
	require.NoError(t, err)
	require.Len(t, removals, 1)
	assert.Equal(t, cronPath(layout), removals[0].File)
	assert.Equal(t, 1, removals[0].Line)
	assert.Equal(t, "0 3 * * * /usr/local/bin/dump.sh --full", removals[0].Text)

	assert.Equal(t, "0 3 * * * /usr/local/bin/dump.sh --full\n", readFile(t, cronPath(layout)),
		"a preview must never write anything")

	require.NoError(t, os.WriteFile(cronPath(layout), []byte("0 9 * * * /usr/local/bin/dump.sh --full\n"), 0o644))
	_, err = PreviewCronRemovals([]string{"dump"}, cfg)
	var mismatch *CronSourceMismatchError
	require.ErrorAs(t, err, &mismatch)
}
