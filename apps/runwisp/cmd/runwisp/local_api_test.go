// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalAPISocketPath_UnderDataDir(t *testing.T) {
	t.Parallel()
	got := localAPISocketPath(Flags{DataDir: "/tmp/runwisp-test-localapi"})
	assert.True(t, strings.HasPrefix(got, "/tmp/runwisp-test-localapi"))
	assert.True(t, strings.HasSuffix(got, "runwisp.sock"))
}

func TestLocalAPISocketPath_ExplicitSocketWins(t *testing.T) {
	t.Parallel()
	// --socket overrides the data-dir default, so a CLI can reach a daemon
	// without restating --data.
	got := localAPISocketPath(Flags{DataDir: "/tmp/ignored", Socket: "/run/rw.sock"})
	assert.Equal(t, "/run/rw.sock", got)
}

func TestFindAppDataDir(t *testing.T) {
	t.Parallel()
	withSocket := func() string {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "runwisp.sock"), nil, 0o600))
		return dir
	}
	def, empty := t.TempDir(), t.TempDir()
	first, second := withSocket(), withSocket()

	assert.Equal(t, def, findAppDataDir(def, []string{empty}), "nothing found keeps the default")
	assert.Equal(t, first, findAppDataDir(def, []string{empty, first, second}), "first candidate with a socket")
	assert.Equal(t, second, findAppDataDir(second, []string{first}), "the default wins when it holds a socket")
}
