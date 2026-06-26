//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/require"
)

// TestHTTPTUI_AuthenticatesAndStreamsLiveLogs is the headline proof for the
// `runwisp tui --url` remote mode: a TUI launched from a FRESH, empty data dir
// — no socket to fall back to — reaches a password-protected daemon purely over
// HTTP, logs in via CHAP using RUNWISP_PASSWORD, renders Home, then triggers a
// task and watches its log lines stream in. Every byte the TUI shows here
// arrived over the HTTP transport.
func TestHTTPTUI_AuthenticatesAndStreamsLiveLogs(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)

	const password = "e2e-http-secret"
	daemon := startDaemonWithEnv(t, projectDir, binaryPath, configPath,
		"RUNWISP_PASSWORD="+password,
	)
	// startDaemonWithEnv only waits for the Unix socket; the TUI dials the TCP
	// listener, so make sure that's actually accepting before we attach.
	daemon.waitForReady(t, apiclient.New(daemon.baseURL, ""), 10*time.Second)

	tui := startHTTPTUI(t, projectDir, binaryPath, configPath, daemon,
		"RUNWISP_PASSWORD="+password,
		"XDG_CACHE_HOME="+filepath.Join(t.TempDir(), "cache"),
	)

	// Reaching Home means CHAP login over HTTP succeeded and the daemon info
	// (tasks, web URL) was fetched over the same connection.
	tui.waitForAll(t, 15*time.Second, "Home", "Web UI", "Open Web UI", "alpha-stream")

	// Drive a real run and confirm its streamed stdout shows up live — the
	// SSE log stream is flowing over HTTP, not a local socket.
	tui.press(t, keyDown, keyEnter)
	tui.waitForAll(t, 5*time.Second, "alpha-stream", "Run Now (r)")

	tui.press(t, "r")
	tui.waitForAll(t, 5*time.Second, "Run Task", "Run 'alpha-stream' now?")
	tui.press(t, "y")

	tui.waitForAll(t, 8*time.Second, "running", "alpha-line-1")
	tui.waitForAll(t, 8*time.Second, "success", "alpha-line-3")
}

// TestHTTPTUI_NoAuthConnectsWithoutPassword mirrors the no-auth contract for
// the HTTP TUI: against a RUNWISP_NO_AUTH daemon the client probes
// /api/auth/status, sees auth isn't required, and connects with no password and
// no prompt.
func TestHTTPTUI_NoAuthConnectsWithoutPassword(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)

	daemon := startDaemonWithEnv(t, projectDir, binaryPath, configPath,
		"RUNWISP_NO_AUTH=1",
	)
	daemon.waitForReady(t, apiclient.New(daemon.baseURL, ""), 10*time.Second)

	tui := startHTTPTUI(t, projectDir, binaryPath, configPath, daemon,
		"XDG_CACHE_HOME="+filepath.Join(t.TempDir(), "cache"),
	)

	tui.waitForAll(t, 15*time.Second, "Home", "Web UI", "alpha-stream")
}

// TestHTTPTUI_WrongPasswordFailsClearly verifies a bad RUNWISP_PASSWORD never
// reaches the UI: with the password fixed by the environment there is nothing
// to re-prompt, so the client fails fast with a human-readable auth error and
// the process exits non-zero.
func TestHTTPTUI_WrongPasswordFailsClearly(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)

	daemon := startDaemonWithEnv(t, projectDir, binaryPath, configPath,
		"RUNWISP_PASSWORD=correct-horse",
	)
	daemon.waitForReady(t, apiclient.New(daemon.baseURL, ""), 10*time.Second)

	tui := startHTTPTUI(t, projectDir, binaryPath, configPath, daemon,
		"RUNWISP_PASSWORD=wrong-password",
		"XDG_CACHE_HOME="+filepath.Join(t.TempDir(), "cache"),
	)

	// The TUI must exit rather than render Home.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tui.exited() {
			break
		}
		time.Sleep(screenPollInterval)
	}
	require.True(t, tui.exited(), "TUI must exit on a wrong password, not start; screen:\n%s", tui.snapshot())

	out := tui.output.Tail(16_000)
	require.Contains(t, out, "authentication with", "the failure must name an auth problem; output:\n%s", out)
	require.Contains(t, out, "rejected the password", "the failure must explain the cause; output:\n%s", out)
}
