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

// TestPromote_CronSourcedTaskLandsInTheRoot: a crontab has no TOML bytes on disk
// to move, so promoting one copies the block the live loader rendered. The crontab
// is left exactly as it was — RunWisp never writes to it.
func TestPromote_CronSourcedTaskLandsInTheRoot(t *testing.T) {
	layout := cronLayout(t, cronRoot, "# nightly dump\n0 3 * * * /usr/local/bin/dump.sh --full\n")
	cronPath := filepath.Join(filepath.Dir(layout.RootPath), "crontabs", "mycron")
	cronBefore := readFile(t, cronPath)

	cfg := loadLayout(t, layout)
	require.Equal(t, model.SourceCron, findTask(t, cfg, "dump").Source)
	require.Equal(t, []string{"dump"}, PromotableNames(cfg))

	res, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.NoError(t, err)
	require.Len(t, res.Promoted, 1)
	assert.False(t, res.StagingRemoved, "there is no staging file in play")

	root := readFile(t, layout.RootPath)
	assert.Contains(t, root, "[tasks.dump]")
	assert.Contains(t, root, "/usr/local/bin/dump.sh --full")
	assert.Contains(t, root, "# my own task, hand-authored", "the operator's own file is untouched")
	assert.Equal(t, cronBefore, readFile(t, cronPath), "the crontab must be byte-identical")
}

// TestPromote_CronSourcedTaskIsNativeAfterReload is the whole point: on the next
// load the operator's TOML and the crontab describe the same job, the cron copy is
// skipped rather than duplicated, and the task reports as native — so the cron line
// can be deleted whenever, or never.
func TestPromote_CronSourcedTaskIsNativeAfterReload(t *testing.T) {
	layout := cronLayout(t, cronRoot, "0 3 * * * /usr/local/bin/dump.sh --full\n")
	cfg := loadLayout(t, layout)
	before := findTask(t, cfg, "dump")
	beforeRun, beforeCron := before.Run, before.Cron

	_, err := Promote(PromoteRequest{Layout: layout, Config: cfg, Names: []string{"dump"}})
	require.NoError(t, err)

	reloaded := loadLayout(t, layout)
	assert.ElementsMatch(t, []string{"mine", "dump"}, taskNames(reloaded),
		"the crontab still lists the job, so a duplicate here means dedup didn't fire")

	after := findTask(t, reloaded, "dump")
	assert.Equal(t, model.SourceNative, after.Source)
	assert.Empty(t, after.SourceFile)
	assert.Equal(t, beforeRun, after.Run, "the promoted definition runs the same command")
	assert.Equal(t, beforeCron, after.Cron)
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
// block is moved out of the staging file and the cron block copied in, and both end
// up in the root.
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

	root := readFile(t, layout.RootPath)
	assert.Contains(t, root, "[tasks.staged_one]")
	assert.Contains(t, root, "[tasks.dump]")
	_, err = os.Stat(layout.StagingPath)
	assert.True(t, os.IsNotExist(err), "the emptied staging file is gone")

	for _, name := range []string{"staged_one", "dump"} {
		assert.Equal(t, model.SourceNative, findTask(t, loadLayout(t, layout), name).Source, name)
	}
}
