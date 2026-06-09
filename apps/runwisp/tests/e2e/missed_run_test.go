//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestMissedRunDetectedAndAlertedOnRestart is the keystone acceptance test for
// the "nothing silently fails" gap: a scheduled run that never executed because
// the daemon was down. It proves the full restart-time path end-to-end against
// the real binary — detection records one browsable missed row, that row raises
// a failure-level in-app alert with zero notification config, and a second
// restart inside the same tick window does NOT re-page.
//
// To stay fast and deterministic we pre-seed a prior run 15 minutes in the past
// rather than idling a real minute for a `* * * * *` tick to elapse; the daemon
// reads that as a 15-tick downtime gap the instant it boots.
func TestMissedRunDetectedAndAlertedOnRestart(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	// Zero-config notifications: with no [notify] block the daemon wires the
	// default in-app catch-all, which now includes run.missed. This is the
	// product promise — a missed scheduled run reaches whoever already gets
	// failure alerts, with no extra configuration. catch_up = "skip" proves
	// detection is independent of the re-run policy.
	configPath := writeNotifyConfig(t, `
[tasks.tick]
cron = "* * * * *"
catch_up = "skip"
run = "true"
`)

	dataDir := testutil.ShortTempDir(t)
	port := reserveTCPPort(t)
	seedAnchorRun(t, dataDir, "tick", -15*time.Minute)

	daemon := startDaemonOn(t, projectDir, binaryPath, configPath, dataDir, port)
	client := socketClient(t, daemon.dataDir)

	// Exactly one browsable missed row is recorded for the downtime gap.
	require.Eventually(t, func() bool {
		s, err := client.GetRunSummary()
		return err == nil && s.Missed == 1
	}, 10*time.Second, 100*time.Millisecond,
		"the daemon must record exactly one missed run row for the gap")

	// It surfaces as a failure-level in-app alert that names the gap.
	missed := waitForMissedNotification(t, client, 10*time.Second)
	require.Equal(t, "error", missed.Severity, "a missed run alerts at failure level")
	require.Equal(t, "tick", missed.TaskName)
	require.Contains(t, missed.Body, "missed", "the body names the missed-run gap")
	require.Contains(t, missed.Body, "daemon was down")

	// Restarting inside the same tick window must NOT re-page: the missed row
	// is now the anchor, so the next boot detects no further gap.
	daemon.stop(t)
	require.True(t, daemon.waitForExit(processExitTimeout), "daemon should exit on stop")

	daemon2 := startDaemonOn(t, projectDir, binaryPath, configPath, dataDir, port)
	client2 := socketClient(t, daemon2.dataDir)
	require.Eventually(t, func() bool { return client2.HealthCheck() == nil },
		10*time.Second, 100*time.Millisecond, "restarted daemon should pass health checks")

	require.Never(t, func() bool {
		s, err := client2.GetRunSummary()
		return err == nil && s.Missed != 1
	}, 3*time.Second, 200*time.Millisecond,
		"a restart with no new gap must not record a second missed row")

	page, err := client2.ListNotifications(50, "")
	require.NoError(t, err)
	require.Equal(t, 1, countMissedNotifications(page.Items),
		"the missed-run alert must not be raised again on a clean restart")
}

// seedAnchorRun inserts a prior successful run directly into the daemon's data
// dir before first boot, dated by offset (negative = in the past). It gives the
// restart-time catch-up a deterministic anchor so a `* * * * *` task reads a
// fixed downtime gap without the test waiting on the wall clock.
func seedAnchorRun(t *testing.T, dataDir, taskName string, offset time.Duration) {
	t.Helper()

	// Mirror flags.DBPath(): the daemon opens <data dir>/runwisp.db.
	db, err := storage.New(filepath.Join(dataDir, "runwisp.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	require.NoError(t, db.CreateRun(context.Background(), &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    taskName,
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByCron,
		CreatedAt:   time.Now().Add(offset),
	}))
}

func waitForMissedNotification(t *testing.T, client *apiclient.Client, timeout time.Duration) server.NotificationDTO {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		page, err := client.ListNotifications(50, "")
		require.NoError(t, err)
		for _, n := range page.Items {
			if n.Kind == "run.missed" {
				return n
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no run.missed notification arrived within %s", timeout)
	return server.NotificationDTO{}
}

func countMissedNotifications(items []server.NotificationDTO) int {
	count := 0
	for _, n := range items {
		if n.Kind == "run.missed" {
			count++
		}
	}
	return count
}
