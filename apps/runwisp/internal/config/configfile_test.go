// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteInit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")

	require.NoError(t, WriteInit(path))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	assert.Equal(t, "hello", cfg.Tasks[0].Name)
}

func TestWriteInitRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")

	require.NoError(t, WriteInit(path))
	err := WriteInit(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestWriteInitForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")

	require.NoError(t, WriteInit(path))
	require.NoError(t, WriteInitForce(path))
}

// TestWriteInitWithCompose verifies the compose-scaffold template loads
// through the normal Load pipeline, expands services from the adjacent
// compose file, and produces tasks named "<alias>.<service>".
func TestWriteInitWithCompose(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web:\n    image: nginx\n  worker:\n    image: alpine\n"),
		0644))
	path := filepath.Join(dir, "runwisp.toml")

	require.NoError(t, WriteInitWithCompose(path, "docker-compose.yml", "myapp"))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 2)
	names := []string{cfg.Tasks[0].Name, cfg.Tasks[1].Name}
	assert.ElementsMatch(t, []string{"myapp.web", "myapp.worker"}, names)
	for i := range cfg.Tasks {
		_, ok := cfg.Tasks[i].ExecutionDef.(*model.ComposeExecution)
		assert.True(t, ok, "task %s should be a ComposeExecution", cfg.Tasks[i].Name)
	}
}
