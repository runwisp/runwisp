//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"context"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestPortConflict_AnotherRunwispReportsIdentity boots a daemon, then launches a
// second `runwisp` against a *different* data dir on the same port. The launcher
// must discover (over the occupied port's GET /api/daemon/identity) that a RunWisp
// daemon holds it and report that daemon's data dir, instead of the generic
// "another process" message. stdin is not a TTY, so it takes the
// non-interactive branch and exits with the identity-rich error.
func TestPortConflict_AnotherRunwispReportsIdentity(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := writeE2EConfig(t, t.TempDir())

	port := reserveTCPPort(t)
	dataA := testutil.ShortTempDir(t)
	startDaemonOn(t, projectDir, binaryPath, configPath, dataA, port)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dataB := testutil.ShortTempDir(t)
	cmd := exec.CommandContext(ctx, binaryPath,
		"--config", configPath,
		"--data", dataB,
		"--port", strconv.Itoa(port),
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv("TERM=dumb")

	out, err := cmd.CombinedOutput()
	output := string(out)

	require.Error(t, err, "second launch must not succeed on an occupied port\noutput:\n%s", output)
	require.Contains(t, output, "another RunWisp daemon",
		"the launcher should recognise the port-holder as RunWisp\noutput:\n%s", output)
	require.Contains(t, output, dataA,
		"the message should name the running daemon's data dir\noutput:\n%s", output)
}
