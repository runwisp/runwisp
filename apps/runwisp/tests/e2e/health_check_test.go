//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/require"
)

// TestServiceHealthCheckRestartsUnhealthyInstance runs a service whose check
// passes while a marker file exists, then fails once the service removes it:
// the instance must end as `unhealthy`, say why in its own log, and be
// replaced by a new one. The probe finds the marker through the service's env,
// which it inherits.
func TestServiceHealthCheckRestartsUnhealthyInstance(t *testing.T) {
	t.Setenv("RUNWISP_AUTH", "off")
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "runwisp.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[services.api]
run = 'touch "$MARKER"; sleep 2; rm -f "$MARKER"; sleep 100'
env = { MARKER = %q }
graceful_stop = "0s"
healthy_after = "5s"

[services.api.health_check]
run = 'test -f "$MARKER" || { echo "marker gone"; exit 1; }'
cron = "@every 1s"
retry_attempts = 0
`, filepath.Join(configDir, "ready"))), 0o600))

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := apiclient.New(daemon.baseURL, "")

	var unhealthy model.Run
	require.Eventually(t, func() bool {
		runs, _, err := client.ListRuns(t.Context(), apiclient.RunsParams{TaskName: "api", Status: string(model.ReasonUnhealthy)})
		if err != nil || len(runs) == 0 {
			return false
		}
		unhealthy = runs[0]
		return true
	}, 15*time.Second, 200*time.Millisecond, "the instance must end as unhealthy once its check fails")

	log := waitForLogContains(t, client, unhealthy.ID, "stopping the instance as unhealthy", 5*time.Second)
	require.Contains(t, log, "health check passed; the instance is healthy")
	require.Contains(t, log, "marker gone")

	require.Eventually(t, func() bool {
		active := activeRuns(t, client, "api")
		return len(active) == 1 && active[0].ID != unhealthy.ID
	}, 10*time.Second, 200*time.Millisecond, "a new instance replaces the unhealthy one")
}
