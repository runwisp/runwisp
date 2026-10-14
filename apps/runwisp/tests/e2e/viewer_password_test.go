//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"bytes"
	"os/exec"
	"strconv"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/stretchr/testify/require"
)

// TestViewerPassword_ReadsAllowedWritesRejected boots a real daemon with both
// passwords, signs in over HTTP with the read-only one, and checks the session
// can list tasks but not trigger one. (The admin login isn't repeated here: the
// per-IP auth rate limit allows only two logins in a row.)
func TestViewerPassword_ReadsAllowedWritesRejected(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := writeE2EConfig(t, t.TempDir())
	daemon := startDaemonWithEnv(t, projectDir, binaryPath, configPath,
		"RUNWISP_PASSWORD=admin-pw-e2e",
		"RUNWISP_VIEWER_PASSWORD=viewer-pw-e2e",
	)

	viewer := apiclient.New(daemon.baseURL, "viewer-pw-e2e")
	require.NoError(t, viewer.Authenticate(t.Context()))
	tasks, err := viewer.ListTasks(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, tasks)
	_, err = viewer.TriggerRun(t.Context(), "alpha-stream", nil, "cli")
	require.ErrorIs(t, err, apiclient.ErrReadOnly)

	// The CLI explains a read-only rejection instead of calling it a disabled trigger.
	cmd := exec.Command(binaryPath, "run", "alpha-stream", "--detach",
		"--url", daemon.baseURL, "--password", "viewer-pw-e2e")
	cmd.Dir = projectDir
	cmd.Env = subprocEnv("XDG_CACHE_HOME=" + t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.Error(t, cmd.Run())
	require.Contains(t, stderr.String(), "read-only")
}

// TestViewerPassword_CmdMintsStableValue covers `runwisp password --viewer` on a
// daemon without RUNWISP_VIEWER_PASSWORD: it mints once, prints the same value
// next time, and that value signs in read-only.
func TestViewerPassword_CmdMintsStableValue(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := writeE2EConfig(t, t.TempDir())
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	viewerCmd := func() string {
		cmd := exec.Command(binaryPath, "--data", daemon.dataDir, "--port", strconv.Itoa(daemon.port), "password", "--viewer")
		cmd.Dir = projectDir
		cmd.Env = subprocEnv()
		out, err := cmd.Output()
		require.NoError(t, err)
		return string(bytes.TrimSpace(out))
	}
	first := viewerCmd()
	require.NotEmpty(t, first)
	require.Equal(t, first, viewerCmd())

	viewer := apiclient.New(daemon.baseURL, first)
	require.NoError(t, viewer.Authenticate(t.Context()))
	_, err := viewer.TriggerRun(t.Context(), "alpha-stream", nil, "cli")
	require.ErrorIs(t, err, apiclient.ErrReadOnly)
}

func TestViewerPassword_CmdRefusesEnvValue(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := writeE2EConfig(t, t.TempDir())
	daemon := startDaemonWithEnv(t, projectDir, binaryPath, configPath,
		"RUNWISP_PASSWORD=admin-pw-e2e", "RUNWISP_VIEWER_PASSWORD=do-not-leak-viewer")

	cmd := exec.Command(binaryPath, "--data", daemon.dataDir, "--port", strconv.Itoa(daemon.port), "password", "--viewer")
	cmd.Dir = projectDir
	cmd.Env = subprocEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.Error(t, cmd.Run())
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "RUNWISP_VIEWER_PASSWORD")
	require.NotContains(t, stderr.String(), "do-not-leak-viewer")
}
