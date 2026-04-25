// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"path/filepath"
	"testing"

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
