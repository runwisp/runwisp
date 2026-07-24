// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
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
	assert.True(t, findTask(t, cfg, "backup").Staged)
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

func TestNewLayout_PutsStagingUnderTheRootConfigDir(t *testing.T) {
	layout := NewLayout(filepath.Join("/etc", "runwisp", "runwisp.toml"))
	assert.Equal(t, filepath.Join("/etc", "runwisp", "runwisp.d", "imported.toml"), layout.StagingPath)
}
