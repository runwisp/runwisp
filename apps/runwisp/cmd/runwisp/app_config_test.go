// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithAppDocumentRejectsEmptyStdin(t *testing.T) {
	t.Parallel()
	_, err := withAppDocument(Flags{}, strings.NewReader(" \n"), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdin was empty")
}

// `--app` swaps where the config comes from and nothing else: the path still
// anchors it.
func TestConfigSourceFollowsAppDocument(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	assert.Equal(t, config.FileSource(path), configSource(Flags{CfgFile: path}))

	f, err := withAppDocument(Flags{CfgFile: path}, strings.NewReader(`{}`), true)
	require.NoError(t, err)
	src, ok := configSource(f).(*config.DocumentSource)
	require.True(t, ok)
	assert.Equal(t, path, src.Path())
}

// validate --app checks the document, never the file at --config.
func TestRunValidateAppDocument(t *testing.T) {
	t.Parallel()
	f := writeValidateConfig(t, "this is not toml")
	f, err := withAppDocument(f, strings.NewReader(`{"tasks":{"digest":{"sdk":true,"cron":"@daily"}}}`), true)
	require.NoError(t, err)
	var out strings.Builder
	require.NoError(t, runValidate(&out, f, false))
	assert.Contains(t, out.String(), "tasks:    1")

	f.ConfigDoc = []byte(`{"tasks":{"digest":{"sdk":true,"run":"true"}}}`)
	err = runValidate(&out, f, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sets both sdk and run")
}
