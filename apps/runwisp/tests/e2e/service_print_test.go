//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIServiceInstallRefusesSystemScopeUnprivileged pins the new default:
// a bare `runwisp service install` targets the system-wide service, so an
// unprivileged run has to stop and say so rather than quietly installing a
// per-user unit the operator didn't ask for. The message must name both
// ways forward, since either could be what they meant.
func TestCLIServiceInstallRefusesSystemScopeUnprivileged(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("autostart is only built for linux and darwin")
	}
	if os.Geteuid() == 0 && runtime.GOOS == "linux" {
		t.Skip("running as root — the system scope is allowed")
	}
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	cmd := exec.Command(binaryPath, "service", "install", "--dry-run")
	cmd.Dir = projectDir
	cmd.Env = subprocEnv()
	out, err := cmd.CombinedOutput()

	require.Errorf(t, err, "expected a refusal, got:\n%s", string(out))
	if runtime.GOOS == "darwin" {
		assert.Contains(t, string(out), "not supported on macOS")
	} else {
		assert.Contains(t, string(out), "requires root")
	}
	assert.Contains(t, string(out), "--local")
}

// TestCLIServicePrintGolden exercises `runwisp service install --print`
// with a fully-specified set of flags so the rendered unit is
// deterministic across machines. The golden body confirms that:
//
//   - the managed marker is the first line (so a `--print > unit.service`
//     drop-in always survives the conflict check on a later install),
//   - ExecStart reflects every flag we passed,
//   - none of the operator's actual $HOME / $PATH leak in (the test
//     pins them explicitly).
//
// The native real-install path needs systemd/launchd in CI, which we
// don't have — this test stays at the rendering layer.
//
// --local because the tests run unprivileged: the default scope is the
// system-wide service, which refuses without root (see
// TestCLIServiceInstallRefusesSystemScopeUnprivileged).
func TestCLIServicePrintGolden(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("autostart is only built for linux and darwin")
	}
	if os.Geteuid() == 0 {
		t.Skip("--local refuses as root on Linux; this golden describes the user unit")
	}
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	args := []string{
		"service", "install", "--print", "--local",
		"--binary", "/opt/runwisp/bin/runwisp",
		"--config", "/etc/runwisp/runwisp.toml",
		"--data", "/var/lib/runwisp",
		"--port", "9477",
		"--host", "127.0.0.1",
	}
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = projectDir
	cmd.Env = append(subprocEnv(),
		"HOME=/home/runwisp",
		"PATH=/usr/local/bin:/usr/bin:/bin",
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "service install --print failed: %s", string(out))

	body := string(out)
	if runtime.GOOS == "linux" {
		assertSystemdGolden(t, body)
	} else {
		assertLaunchdGolden(t, body)
	}
}

func assertSystemdGolden(t *testing.T, body string) {
	t.Helper()
	want := strings.Join([]string{
		"# Managed by runwisp service install — DO NOT EDIT",
		"# runwisp-config-hash: ",
		"# runwisp-binary-sha256: ",
		"[Unit]",
		"Description=RunWisp — lightweight cron daemon",
		"Documentation=https://docs.runwisp.com/",
		"After=network.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=/opt/runwisp/bin/runwisp daemon --config /etc/runwisp/runwisp.toml --data /var/lib/runwisp --port 9477 --host 127.0.0.1",
		"Restart=on-failure",
		"RestartSec=5s",
		"KillMode=mixed",
		"TimeoutStopSec=30s",
		"Environment=HOME=/home/runwisp",
		"Environment=PATH=/usr/local/bin:/usr/bin:/bin",
		"Environment=LANG=C.UTF-8",
		"",
		"[Install]",
		"WantedBy=default.target",
	}, "\n")
	for _, line := range strings.Split(want, "\n") {
		require.Containsf(t, body, line, "rendered unit missing line: %q\nfull output:\n%s", line, body)
	}
}

func assertLaunchdGolden(t *testing.T, body string) {
	t.Helper()
	for _, line := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		"<!-- Managed by runwisp service install — DO NOT EDIT -->",
		"<key>Label</key>",
		// Label is per-instance (`com.runwisp.daemon.<fingerprint>`) so
		// the full string isn't deterministic across machines — just
		// confirm the prefix is present.
		"<string>com.runwisp.daemon.",
		"<string>/opt/runwisp/bin/runwisp</string>",
		"<string>daemon</string>",
		"<string>--config</string>",
		"<string>/etc/runwisp/runwisp.toml</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>",
	} {
		require.Containsf(t, body, line, "rendered plist missing line: %q\nfull output:\n%s", line, body)
	}
}
