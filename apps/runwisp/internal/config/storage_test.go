// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Storage characterization tests. [storage] is parsed but intentionally has no
// Validate() pass beyond the byte-size parsing in parseScopedByteSize:
// max_size (the log-dir total cap consumed by RetentionCleaner) and
// min_free_space (the filesystem free-disk floor consumed by the executor)
// measure independent axes, so there is deliberately no cross-field check such
// as max_size < min_free_space — that comparison would be semantically wrong.
// These tests lock the parse behavior and the absence of cross-field rules.

func TestStorage_UnitsParse(t *testing.T) {
	path := writeTOML(t, `
[storage]
max_size = "10mb"
min_free_space = "500mb"

[tasks.t]
run = "echo hi"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, int64(10*1024*1024), cfg.Storage.MaxSize)
	assert.Equal(t, int64(500*1024*1024), cfg.Storage.MinFreeSpace)
}

func TestStorage_OmittedIsZero(t *testing.T) {
	path := writeTOML(t, `
[tasks.t]
run = "echo hi"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.Storage.MaxSize)
	assert.Equal(t, int64(0), cfg.Storage.MinFreeSpace)
}

func TestStorage_ExplicitZero(t *testing.T) {
	path := writeTOML(t, `
[storage]
max_size = "0"
min_free_space = "0"

[tasks.t]
run = "echo hi"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.Storage.MaxSize)
	assert.Equal(t, int64(0), cfg.Storage.MinFreeSpace)
}

// max_size and min_free_space are unrelated axes — a "small cap, large floor"
// pairing that a cross-field check would flag is perfectly valid and loads.
func TestStorage_NoCrossFieldValidation(t *testing.T) {
	path := writeTOML(t, `
[storage]
max_size = "1mb"
min_free_space = "10gb"

[tasks.t]
run = "echo hi"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, int64(1*1024*1024), cfg.Storage.MaxSize)
	assert.Equal(t, int64(10*1024*1024*1024), cfg.Storage.MinFreeSpace)
}

func TestStorage_NegativeRejected(t *testing.T) {
	tests := []struct {
		name  string
		toml  string
		scope string
	}{
		{
			name:  "max_size",
			toml:  "[storage]\nmax_size = \"-1\"\n[tasks.t]\nrun = \"echo hi\"\n",
			scope: "storage.max_size",
		},
		{
			name:  "min_free_space",
			toml:  "[storage]\nmin_free_space = \"-1\"\n[tasks.t]\nrun = \"echo hi\"\n",
			scope: "storage.min_free_space",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeTOML(t, tt.toml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.scope)
		})
	}
}

func TestStorage_GarbageRejected(t *testing.T) {
	tests := []struct {
		name  string
		toml  string
		scope string
	}{
		{
			name:  "max_size",
			toml:  "[storage]\nmax_size = \"abc\"\n[tasks.t]\nrun = \"echo hi\"\n",
			scope: "storage.max_size",
		},
		{
			name:  "min_free_space",
			toml:  "[storage]\nmin_free_space = \"abc\"\n[tasks.t]\nrun = \"echo hi\"\n",
			scope: "storage.min_free_space",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeTOML(t, tt.toml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.scope)
		})
	}
}
