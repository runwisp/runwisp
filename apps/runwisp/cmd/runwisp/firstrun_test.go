// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptAndScaffoldAcceptsYes(t *testing.T) {
	cases := []string{"y\n", "Y\n", "yes\n", "YES\n", "\n", "  y  \n"}
	for _, answer := range cases {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runwisp.toml")
			var out bytes.Buffer

			require.NoError(t, promptAndScaffold(path, strings.NewReader(answer), &out))

			assert.FileExists(t, path)
			assert.Contains(t, out.String(), "No runwisp.toml at "+filepath.Dir(path))
			assert.Contains(t, out.String(), "Created "+path)
		})
	}
}

func TestPromptAndScaffoldDeclines(t *testing.T) {
	for _, answer := range []string{"n\n", "no\n", "N\n", "anything-else\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runwisp.toml")
			var out bytes.Buffer

			err := promptAndScaffold(path, strings.NewReader(answer), &out)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "no runwisp.toml at "+filepath.Dir(path))
			assert.NoFileExists(t, path)
		})
	}
}

func TestScaffoldIfMissingNoopWhenFileExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte("[tasks.x]\nrun = \"true\"\n"), 0644))

	assert.NoError(t, scaffoldIfMissing(path))
}
