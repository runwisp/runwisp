// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const stagedTask = "[tasks.backup]\ncron = \"0 3 * * *\"\nrun = \"echo backup\"\n"

func TestStage_GreenfieldCreatesRootAndStaging(t *testing.T) {
	dir := t.TempDir()
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	res, err := Stage(StageRequest{Layout: layout, Staging: []byte(stagedTask), Validate: true})
	require.NoError(t, err)
	assert.Equal(t, RootCreated, res.Root)
	assert.Equal(t, layout.StagingPath, res.StagingPath)

	cfg, err := config.Load(layout.RootPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"backup"}, taskNames(cfg))
	assert.True(t, findTask(t, cfg, "backup").Source == model.SourceStaged)
}

func TestStage_BrownfieldWiresIncludeAndKeepsOperatorBytes(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": "[tasks.native]\nrun = \"echo native\"\n",
	})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	res, err := Stage(StageRequest{Layout: layout, Staging: []byte(stagedTask), Validate: true})
	require.NoError(t, err)
	assert.Equal(t, RootWired, res.Root)

	root := readFile(t, layout.RootPath)
	assert.Contains(t, root, "[tasks.native]\nrun = \"echo native\"\n", "the operator's own bytes must survive")
	assert.Contains(t, root, `include = ["runwisp.d/*.toml"]`)

	cfg, err := config.Load(layout.RootPath)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"native", "backup"}, taskNames(cfg))
}

func TestStage_AlreadyIncludedLeavesRootByteIdentical(t *testing.T) {
	rootTOML := "[daemon]\ninclude = [\"runwisp.d/*.toml\"]\n\n[tasks.native]\nrun = \"echo native\"\n"
	dir := writeFileTree(t, map[string]string{"runwisp.toml": rootTOML})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	res, err := Stage(StageRequest{Layout: layout, Staging: []byte(stagedTask), Validate: true})
	require.NoError(t, err)
	assert.Equal(t, RootAlreadyIncluded, res.Root)
	assert.Equal(t, rootTOML, readFile(t, layout.RootPath))
}

func TestStage_RefusesCustomIncludeWithoutWriting(t *testing.T) {
	rootTOML := "[daemon]\ninclude = [\"conf.d/*.toml\"]\n"
	dir := writeFileTree(t, map[string]string{"runwisp.toml": rootTOML})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	_, err := Stage(StageRequest{Layout: layout, Staging: []byte(stagedTask), Validate: true})
	require.ErrorIs(t, err, ErrIncludeNeedsManualWiring)

	assert.Equal(t, rootTOML, readFile(t, layout.RootPath))
	_, statErr := os.Stat(layout.StagingPath)
	assert.True(t, os.IsNotExist(statErr), "a refused wiring must not leave a staging file behind")
}

// TestStage_ConflictRollsBackBothFiles covers the case the atomicity exists for:
// the import defines a name the operator's own config already uses, so the merged
// config stops loading. Nothing may survive — least of all a staging file the
// daemon would choke on.
func TestStage_ConflictRollsBackBothFiles(t *testing.T) {
	rootTOML := "[tasks.backup]\nrun = \"echo mine\"\n"
	dir := writeFileTree(t, map[string]string{"runwisp.toml": rootTOML})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	_, err := Stage(StageRequest{Layout: layout, Staging: []byte(stagedTask), Validate: true})
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Contains(t, conflict.Err.Error(), "backup")

	assert.Equal(t, rootTOML, readFile(t, layout.RootPath))
	_, statErr := os.Stat(layout.StagingPath)
	assert.True(t, os.IsNotExist(statErr))
}

// TestStage_AlreadyInvalidRootIsNotBlamedOnTheImport is the honesty fix: when the
// root config didn't load *before* the write either, saying "this import
// conflicts with your config" sends the operator hunting for a clash that
// doesn't exist.
func TestStage_AlreadyInvalidRootIsNotBlamedOnTheImport(t *testing.T) {
	// A valid-TOML config that fails validation: a cron expression that can't
	// parse. The include wiring succeeds; the load gate still refuses.
	rootTOML := "[tasks.broken]\ncron = \"not a cron expression\"\nrun = \"echo hi\"\n"
	dir := writeFileTree(t, map[string]string{"runwisp.toml": rootTOML})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	_, err := Stage(StageRequest{Layout: layout, Staging: []byte(stagedTask), Validate: true})
	var preexisting *PreexistingError
	require.ErrorAs(t, err, &preexisting, "an already-broken config must not be reported as a conflict")

	var conflict *ConflictError
	assert.NotErrorAs(t, err, &conflict)
	assert.Equal(t, rootTOML, readFile(t, layout.RootPath))
}

// TestStage_ValidateFalseKeepsFilesForTheOperator covers content that is known
// not to validate — an unmappable cron line that became a `# TODO`. Rolling that
// back would throw away the only record of what needs fixing.
func TestStage_ValidateFalseKeepsFilesForTheOperator(t *testing.T) {
	dir := t.TempDir()
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))
	broken := "[tasks.mystery]\ncron = \"@every second thursday\"  # TODO: couldn't map this schedule\nrun = \"echo hi\"\n"

	_, err := Stage(StageRequest{Layout: layout, Staging: []byte(broken), Validate: false})
	require.NoError(t, err)

	assert.Contains(t, readFile(t, layout.StagingPath), "# TODO")
	_, loadErr := config.Load(layout.RootPath)
	assert.Error(t, loadErr, "the point of this case is that the config does not load yet")
}

func TestStage_OverwritesTheMachineOwnedStagingFile(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":            "[daemon]\ninclude = [\"runwisp.d/*.toml\"]\n",
		"runwisp.d/imported.toml": "[tasks.stale]\nrun = \"echo stale\"\n",
	})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	_, err := Stage(StageRequest{Layout: layout, Staging: []byte(stagedTask), Validate: true})
	require.NoError(t, err)

	cfg, err := config.Load(layout.RootPath)
	require.NoError(t, err)
	assert.Equal(t, []string{"backup"}, taskNames(cfg), "staging is machine-owned and replaced wholesale")
}

// TestPlanStageMatchesStage is what makes `--dry-run` trustworthy: for every
// shape the root config comes in, the plan reports the same outcome the real
// write goes on to report. A plan that predicts one thing and a write that does
// another is worse than no plan at all.
func TestPlanStageMatchesStage(t *testing.T) {
	roots := map[string]map[string]string{
		"greenfield":      {},
		"needs wiring":    {"runwisp.toml": "[tasks.native]\nrun = \"echo native\"\n"},
		"already wired":   {"runwisp.toml": "[daemon]\ninclude = [\"runwisp.d/*.toml\"]\n"},
		"already invalid": {"runwisp.toml": "[tasks.broken]\ncron = \"not a cron expression\"\nrun = \"echo hi\"\n"},
	}
	for name, tree := range roots {
		t.Run(name, func(t *testing.T) {
			dir := writeFileTree(t, tree)
			layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

			plan, planErr := PlanStage(layout)
			require.NoError(t, planErr)
			// Planning is read-only, so the real Stage below starts from the same tree
			// the plan was made against.
			before := testutil.SnapshotTree(t, dir)

			// Validate false: this test is about the root outcome, not the load gate,
			// and the "already invalid" root would fail that gate by design.
			staged, err := Stage(StageRequest{Layout: layout, Staging: []byte(stagedTask), Validate: false})
			require.NoError(t, err)

			assert.Equal(t, staged.Root, plan.Root, "the plan promised a different root outcome")
			assert.Equal(t, staged.StagingPath, plan.StagingPath)
			if staged.PreLoadErr == nil {
				assert.NoError(t, plan.PreLoadErr)
			} else {
				assert.EqualError(t, plan.PreLoadErr, staged.PreLoadErr.Error())
			}
			assert.NotEqual(t, before, testutil.SnapshotTree(t, dir), "the real Stage should have written something")
		})
	}
}

// TestPlanStageWritesNothing pins the read-only half directly, including the
// staging directory a real Stage would create.
func TestPlanStageWritesNothing(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"runwisp.toml": "[tasks.native]\nrun = \"echo native\"\n"})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))
	before := testutil.SnapshotTree(t, dir)

	_, err := PlanStage(layout)
	require.NoError(t, err)
	assert.Equal(t, before, testutil.SnapshotTree(t, dir))
	_, statErr := os.Stat(filepath.Dir(layout.StagingPath))
	assert.True(t, os.IsNotExist(statErr), "planning must not create the staging dir")
}

// TestPlanStageRefusesACustomInclude: a root the real write can't wire is
// something the plan has to refuse too, in the same words.
func TestPlanStageRefusesACustomInclude(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"runwisp.toml": "[daemon]\ninclude = [\"conf.d/*.toml\"]\n"})
	layout := NewLayout(filepath.Join(dir, "runwisp.toml"))

	_, err := PlanStage(layout)
	require.ErrorIs(t, err, ErrIncludeNeedsManualWiring)
}

func TestNewLayout_PutsStagingUnderTheRootConfigDir(t *testing.T) {
	layout := NewLayout(filepath.Join("/etc", "runwisp", "runwisp.toml"))
	assert.Equal(t, filepath.Join("/etc", "runwisp", "runwisp.d", "imported.toml"), layout.StagingPath)
}
