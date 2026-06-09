// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShellBackend_UmaskAppliedInChild proves the umask wrap reaches the child:
// the `umask` builtin prints the configured mask, not the daemon's.
func TestShellBackend_UmaskAppliedInChild(t *testing.T) {
	task := &model.Task{Name: "um"}
	got := drainStdout(t, task, &model.ShellExecution{Script: "umask", Umask: "0027"})
	assert.Contains(t, strings.TrimSpace(got), "027")
}

// TestShellBackend_UmaskAffectsFileMode is the bug-first proof: a file created
// by the run lands with permissions masked by the configured umask. Without the
// wrap the file would carry the daemon's (typically 0022) mask instead.
func TestShellBackend_UmaskAffectsFileMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "created")
	task := &model.Task{Name: "um"}
	// 0077 masks all group/other bits; a 0666 base create yields 0600.
	drainStdout(t, task, &model.ShellExecution{
		Script: "rm -f " + target + "; : > " + target,
		Umask:  "0077",
	})

	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "0077 umask should strip group/other bits")
}

// TestShellBackend_NoUmaskLeavesScriptUnwrapped confirms the common path is
// untouched: with no umask the script runs verbatim.
func TestShellBackend_NoUmaskLeavesScriptUnwrapped(t *testing.T) {
	task := &model.Task{Name: "um"}
	got := drainStdout(t, task, &model.ShellExecution{Script: "echo plain"})
	assert.Equal(t, "plain", strings.TrimSpace(got))
}
