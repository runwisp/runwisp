//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestNoAuth_UnauthenticatedTCPAccess boots a real daemon with
// RUNWISP_AUTH=off and exercises the passwordless contract end to end:
// protected endpoints answer unauthenticated TCP requests, the auth status
// endpoint reports auth_required=false (what the web UI keys off), the
// startup banner warns loudly, and `runwisp password` refuses with its
// dedicated exit code.
func TestNoAuth_UnauthenticatedTCPAccess(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	daemon := startDaemonWithEnv(t, projectDir, binaryPath, configPath,
		"RUNWISP_AUTH=off",
	)

	// Protected endpoint over TCP with no JWT, no cookie, no Authenticate call.
	client := apiclient.New(daemon.baseURL, "")
	tasks, err := client.ListTasks()
	require.NoError(t, err,
		"unauthenticated TCP requests must reach protected routes when RUNWISP_AUTH=off")
	require.NotEmpty(t, tasks)

	// Auth status must report auth_required=false so the UI skips login.
	resp, err := http.Get(daemon.baseURL + "/api/auth/status")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var status struct {
		AuthRequired  bool `json:"auth_required"`
		Authenticated bool `json:"authenticated"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	require.False(t, status.AuthRequired)
	require.True(t, status.Authenticated)

	// The startup warning must be unmissable in the daemon's output.
	require.Contains(t, daemon.output.Tail(1<<20), "Authentication is DISABLED",
		"the no-auth security banner must appear in daemon output")

	// `runwisp password` has nothing to print — dedicated exit code 5.
	stdout, stderr, exitCode := runPasswordCmd(t, binaryPath, projectDir, daemon.dataDir, daemon.port, nil)
	require.Equal(t, 5, exitCode, "no-auth must exit with its dedicated code; stderr:\n%s", stderr)
	require.Empty(t, stdout, "no password value may be printed when auth is disabled")
	require.Contains(t, stderr, "RUNWISP_AUTH=off")
}

// TestNoAuth_ConflictWithPasswordRefusesToBoot asserts the daemon rejects the
// contradictory RUNWISP_AUTH=off + RUNWISP_PASSWORD combination at startup
// instead of silently picking one.
func TestNoAuth_ConflictWithPasswordRefusesToBoot(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	dataDir := testutil.ShortTempDir(t)
	port := reserveTCPPort(t)

	cmd := exec.Command(
		binaryPath,
		"--config", configPath,
		"--data", dataDir,
		"--port", strconv.Itoa(port),
		"daemon",
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv(
		"TERM=xterm-256color",
		"RUNWISP_AUTH=off",
		"RUNWISP_PASSWORD=contradictory",
	)

	done := make(chan error, 1)
	output := &lockedBuffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	require.NoError(t, cmd.Start())
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		require.Error(t, err, "daemon must exit non-zero on the env conflict")
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("daemon did not exit on RUNWISP_AUTH=off + RUNWISP_PASSWORD conflict; output:\n%s",
			output.Tail(1<<20))
	}
	require.Contains(t, output.Tail(1<<20), "mutually exclusive",
		"the startup error must name the conflict")
}
