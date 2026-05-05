//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/require"
)

// TestNotificationsOutboundFiresAndCoalesces covers the success path: a
// failing run produces a Slack webhook POST, an in-app row, and an SSE
// notification.created event. A second failure coalesces into the same row
// (count==2) and emits a notification.updated event.
func TestNotificationsOutboundFiresAndCoalesces(t *testing.T) {
	received := make(chan []byte, 8)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case received <- body:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhook.Close)

	configPath := writeNotifyConfig(t, fmt.Sprintf(`
[notify]
coalesce_window = "1m"

[tasks.fail-task]
run = "exit 1"

[[notifier]]
id          = "ops"
type        = "slack"
webhook_url = "%s"

[[notification_route]]
match  = { kind = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["ops", "inapp"]
`, webhook.URL))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	password := waitForPassword(t, daemon.dataDir)
	client := apiclient.New(daemon.baseURL, password)
	require.NoError(t, client.Authenticate())

	streamCtx, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	events, err := client.StreamNotifications(streamCtx)
	require.NoError(t, err)
	waitForFirstPing(t, events)

	_, err = client.TriggerRun("fail-task")
	require.NoError(t, err)

	body := waitForWebhook(t, received, 10*time.Second)
	require.Contains(t, string(body), "fail-task",
		"slack body should mention the failing task")

	created := waitForNotificationEvent(t, events, "notification.created", 10*time.Second)
	require.Equal(t, "fail-task", created.TaskName)
	require.Equal(t, "error", created.Severity)
	require.Equal(t, "run.failed", created.Kind)

	page := waitForListedNotifications(t, client, 1, 5*time.Second)
	require.Equal(t, "fail-task", page.Items[0].TaskName)
	require.Equal(t, "run.failed", page.Items[0].Kind)
	require.Equal(t, 1, page.Items[0].Count)

	// Second failure on the same task within the window should coalesce.
	_, err = client.TriggerRun("fail-task")
	require.NoError(t, err)

	updated := waitForNotificationEvent(t, events, "notification.updated", 10*time.Second)
	require.Equal(t, created.ID, updated.ID, "must reuse the same row id")
	require.GreaterOrEqual(t, updated.Count, 2)

	// And the second slack POST must still arrive.
	waitForWebhook(t, received, 5*time.Second)
}

// TestNotificationsDeliveryFailureSurfacesInApp covers the dispatcher's
// reportDeliveryFailure synthesizer: when an outbound channel exhausts its
// retry budget, the daemon must surface a notify.delivery_failed in-app row
// (so a broken webhook never silently rots).
func TestNotificationsDeliveryFailureSurfacesInApp(t *testing.T) {
	t.Setenv("RUNWISP_NOTIFY_MAX_ELAPSED", "1500ms")

	var hits atomic.Int64
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"simulated"}`))
	}))
	t.Cleanup(webhook.Close)

	configPath := writeNotifyConfig(t, fmt.Sprintf(`
[tasks.fail-task]
run = "exit 1"

[[notifier]]
id          = "broken"
type        = "slack"
webhook_url = "%s"

[[notification_route]]
match  = { kind = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["broken", "inapp"]
`, webhook.URL))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	password := waitForPassword(t, daemon.dataDir)
	client := apiclient.New(daemon.baseURL, password)
	require.NoError(t, client.Authenticate())

	_, err := client.TriggerRun("fail-task")
	require.NoError(t, err)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		page, err := client.ListNotifications(50, "")
		require.NoError(t, err)
		if hasDeliveryFailure(page.Items) && len(page.Items) >= 2 {
			require.GreaterOrEqual(t, hits.Load(), int64(1),
				"webhook must have been hit at least once before giving up")
			return
		}
		time.Sleep(150 * time.Millisecond)
	}

	page, _ := client.ListNotifications(50, "")
	t.Fatalf("notify.delivery_failed never appeared. webhook hits=%d, items=%+v",
		hits.Load(), page.Items)
}

// TestNotificationsZeroConfigInappFires verifies the zero-setup default: a
// TOML with no [notify] section, no [[notifier]] blocks, no
// [[notification_route]] rules, and no per-task notify_on_failure still
// produces an in-app notification when a run fails. The bell in the Web UI
// and the footer line in the TUI must light up out of the box.
func TestNotificationsZeroConfigInappFires(t *testing.T) {
	configPath := writeNotifyConfig(t, `
[tasks.fail-task]
run = "exit 1"
`)

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	password := waitForPassword(t, daemon.dataDir)
	client := apiclient.New(daemon.baseURL, password)
	require.NoError(t, client.Authenticate())

	streamCtx, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	events, err := client.StreamNotifications(streamCtx)
	require.NoError(t, err)
	waitForFirstPing(t, events)

	_, err = client.TriggerRun("fail-task")
	require.NoError(t, err)

	created := waitForNotificationEvent(t, events, "notification.created", 10*time.Second)
	require.Equal(t, "fail-task", created.TaskName)
	require.Equal(t, "run.failed", created.Kind)

	page := waitForListedNotifications(t, client, 1, 5*time.Second)
	require.Equal(t, "fail-task", page.Items[0].TaskName)
	require.Equal(t, "run.failed", page.Items[0].Kind)
}

// ---------- helpers ----------

func writeNotifyConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.notify.toml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func waitForFirstPing(t *testing.T, events <-chan apiclient.NotificationStreamEvent) {
	t.Helper()
	select {
	case ev, ok := <-events:
		require.True(t, ok, "stream closed before any event arrived")
		require.Equal(t, "ping", ev.Type, "first SSE message should be a ping")
	case <-time.After(5 * time.Second):
		t.Fatal("never received initial SSE ping")
	}
}

func waitForWebhook(t *testing.T, received <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case body := <-received:
		return body
	case <-time.After(timeout):
		t.Fatalf("webhook never received a request within %s", timeout)
		return nil
	}
}

func waitForNotificationEvent(
	t *testing.T,
	events <-chan apiclient.NotificationStreamEvent,
	wanted string,
	timeout time.Duration,
) apiclient.Notification {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("stream closed before %s arrived", wanted)
			}
			if ev.Type != wanted {
				continue
			}
			notif, err := apiclient.DecodeNotificationEnvelope(ev.Data)
			require.NoError(t, err)
			return notif
		case <-deadline:
			t.Fatalf("never received SSE event %q", wanted)
		}
	}
}

func waitForListedNotifications(
	t *testing.T,
	client *apiclient.Client,
	atLeast int,
	timeout time.Duration,
) apiclient.NotificationsPage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		page, err := client.ListNotifications(50, "")
		require.NoError(t, err)
		if len(page.Items) >= atLeast {
			return page
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("notifications list never reached %d items within %s", atLeast, timeout)
	return apiclient.NotificationsPage{}
}

func hasDeliveryFailure(items []apiclient.Notification) bool {
	for _, n := range items {
		if n.Kind == "notify.delivery_failed" {
			return true
		}
	}
	return false
}
