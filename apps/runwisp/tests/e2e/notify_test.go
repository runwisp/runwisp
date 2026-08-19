//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/stretchr/testify/require"
)

// TestNotificationsOutboundFiresAndCoalesces covers the success path: a
// failing run produces a Slack webhook POST, an in-app row, and an SSE
// notification.created event. A second failure within the coalescing window
// folds into the same in-app row (count==2) AND must NOT fire a second
// outbound webhook — outbound coalescing protects the paging surface from
// flapping-task storms. A separate test below verifies the
// `coalesce_outbound = false` opt-out.
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
match  = { kinds = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["ops", "inapp"]
`, webhook.URL))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	events, err := client.StreamNotifications(streamCtx)
	require.NoError(t, err)
	waitForFirstPing(t, events)

	_, err = client.TriggerRun("fail-task", nil)
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

	// Second failure on the same task within the window:
	//   - the in-app row must coalesce (notification.updated, count >= 2);
	//   - the outbound webhook must NOT fire — that's the whole point of
	//     outbound coalescing.
	_, err = client.TriggerRun("fail-task", nil)
	require.NoError(t, err)

	updated := waitForNotificationEvent(t, events, "notification.updated", 10*time.Second)
	require.Equal(t, created.ID, updated.ID, "must reuse the same row id")
	require.GreaterOrEqual(t, updated.Count, 2)

	// Drain anything that might have raced into the channel before the
	// in-app coalescer fired, then assert the webhook stays silent for the
	// rest of the window.
	select {
	case <-received:
		t.Fatal("outbound webhook fired a second time within the coalescing window — outbound coalescing is broken")
	case <-time.After(2 * time.Second):
	}
}

// TestNotificationsGenericWebhookFires covers the generic webhook channel
// end-to-end: a failing run must produce exactly one POST to the configured
// URL with the default JSON template body, the application/json content
// type, and any custom headers from the TOML passed through verbatim.
func TestNotificationsGenericWebhookFires(t *testing.T) {
	type capturedRequest struct {
		method      string
		contentType string
		authHeader  string
		body        []byte
	}
	received := make(chan capturedRequest, 8)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case received <- capturedRequest{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			authHeader:  r.Header.Get("Authorization"),
			body:        body,
		}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	configPath := writeNotifyConfig(t, fmt.Sprintf(`
[tasks.fail-task]
run = "exit 1"

[[notifier]]
id      = "hook"
type    = "webhook"
url     = "%s"
headers = { Authorization = "Bearer e2e-test-token" }

[[notification_route]]
match  = { kinds = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["hook"]
`, hook.URL))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	_, err := client.TriggerRun("fail-task", nil)
	require.NoError(t, err)

	var req capturedRequest
	select {
	case req = <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("webhook never received a request within 10s")
	}

	require.Equal(t, http.MethodPost, req.method)
	require.Equal(t, "application/json", req.contentType)
	require.Equal(t, "Bearer e2e-test-token", req.authHeader,
		"custom headers from TOML must pass through verbatim")

	var payload struct {
		Kind     string `json:"kind"`
		Severity string `json:"severity"`
		Task     string `json:"task"`
		Run      struct {
			ID       string `json:"id"`
			ExitCode int    `json:"exit_code"`
		} `json:"run"`
	}
	require.NoError(t, json.Unmarshal(req.body, &payload),
		"webhook body must be valid JSON, got: %s", req.body)
	require.Equal(t, "run.failed", payload.Kind)
	require.Equal(t, "error", payload.Severity)
	require.Equal(t, "fail-task", payload.Task)
	require.NotEmpty(t, payload.Run.ID)
	require.Equal(t, 1, payload.Run.ExitCode)
}

// TestNotificationsOutboundCoalesceOptOut verifies the rare opt-out path:
// `coalesce_outbound = false` restores per-event delivery for users who
// genuinely want a webhook hit per failure (e.g., piping into their own
// aggregator).
func TestNotificationsOutboundCoalesceOptOut(t *testing.T) {
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
coalesce_window   = "1m"
coalesce_outbound = false

[tasks.fail-task]
run = "exit 1"

[[notifier]]
id          = "ops"
type        = "slack"
webhook_url = "%s"

[[notification_route]]
match  = { kinds = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["ops"]
`, webhook.URL))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	_, err := client.TriggerRun("fail-task", nil)
	require.NoError(t, err)
	waitForWebhook(t, received, 10*time.Second)

	_, err = client.TriggerRun("fail-task", nil)
	require.NoError(t, err)
	waitForWebhook(t, received, 10*time.Second)
}

// TestNotificationsDeliveryFailureSurfacesInApp covers the dispatcher's
// reportDeliveryFailure synthesizer: when an outbound channel exhausts its
// retry budget, the daemon must surface a notify.delivery_failed in-app row
// (so a broken webhook never silently rots).
func TestNotificationsDeliveryFailureSurfacesInApp(t *testing.T) {
	var hits atomic.Int64
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"simulated"}`))
	}))
	t.Cleanup(webhook.Close)

	configPath := writeNotifyConfig(t, fmt.Sprintf(`
[notify]
retry_budget = "1500ms"

[tasks.fail-task]
run = "exit 1"

[[notifier]]
id          = "broken"
type        = "slack"
webhook_url = "%s"

[[notification_route]]
match  = { kinds = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["broken", "inapp"]
`, webhook.URL))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	_, err := client.TriggerRun("fail-task", nil)
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

	client := socketClient(t, daemon.dataDir)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	events, err := client.StreamNotifications(streamCtx)
	require.NoError(t, err)
	waitForFirstPing(t, events)

	_, err = client.TriggerRun("fail-task", nil)
	require.NoError(t, err)

	created := waitForNotificationEvent(t, events, "notification.created", 10*time.Second)
	require.Equal(t, "fail-task", created.TaskName)
	require.Equal(t, "run.failed", created.Kind)

	page := waitForListedNotifications(t, client, 1, 5*time.Second)
	require.Equal(t, "fail-task", page.Items[0].TaskName)
	require.Equal(t, "run.failed", page.Items[0].Kind)
}

// TestNotificationsSMTPDeliversAndCoalesces verifies the SMTP channel
// end-to-end: a failing run produces a real SMTP transaction with both a
// text/plain and a text/html part, mentioning the task; a second failure
// within the coalesce window must NOT trigger a second SMTP transaction
// (outbound coalescing applies to SMTP exactly like Slack).
func TestNotificationsSMTPDeliversAndCoalesces(t *testing.T) {
	srv := newTestSMTPServer(t)
	t.Cleanup(srv.Close)

	host, portStr, err := net.SplitHostPort(srv.Addr())
	require.NoError(t, err)

	configPath := writeNotifyConfig(t, fmt.Sprintf(`
[notify]
coalesce_window = "1m"

[tasks.fail-task]
run = "exit 1"

[[notifier]]
id   = "email-ops"
type = "smtp"
host = "%s"
port = %s
tls  = "off"
from = "RunWisp <runwisp@example.test>"
to   = ["alerts@example.test"]

[[notification_route]]
match  = { kinds = ["run.failed", "run.timeout", "run.crashed"] }
notify = ["email-ops"]
`, host, portStr))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	_, err = client.TriggerRun("fail-task", nil)
	require.NoError(t, err)

	body := srv.WaitForMessage(t, 15*time.Second)
	require.Contains(t, body, "fail-task", "SMTP DATA should mention the failing task")
	require.Contains(t, body, "Content-Type: text/plain", "must include the plain-text alternative")
	require.Contains(t, body, "Content-Type: text/html", "must include the HTML alternative")
	require.Contains(t, body, "multipart/alternative", "must be multipart/alternative")

	// Second failure within the coalesce window: no further SMTP transaction.
	_, err = client.TriggerRun("fail-task", nil)
	require.NoError(t, err)

	srv.ExpectNoMessage(t, 2*time.Second)
}

// ---------- SMTP test server ----------

// testSMTPServer is a tiny stdlib-only fake that accepts EHLO → MAIL → RCPT →
// DATA → QUIT and captures the message body. It speaks just enough of the
// SMTP protocol to satisfy go-mail with TLS disabled (`tls = "off"`).
type testSMTPServer struct {
	ln     net.Listener
	bodies chan string
	wg     sync.WaitGroup
	closed atomic.Bool
}

func newTestSMTPServer(t *testing.T) *testSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &testSMTPServer{ln: ln, bodies: make(chan string, 8)}
	s.wg.Add(1)
	go s.acceptLoop()
	return s
}

func (s *testSMTPServer) Addr() string { return s.ln.Addr().String() }

func (s *testSMTPServer) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *testSMTPServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.handle(conn)
	}
}

func (s *testSMTPServer) handle(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()
	sess := &smtpTestSession{
		br:     bufio.NewReader(conn),
		bw:     bufio.NewWriter(conn),
		bodies: s.bodies,
	}
	sess.run()
}

// smtpTestSession holds per-connection state for the fake SMTP server. The
// split into command vs. data-mode handlers keeps each step small enough to
// reason about (and below SonarCloud's cognitive-complexity threshold).
type smtpTestSession struct {
	br     *bufio.Reader
	bw     *bufio.Writer
	bodies chan<- string
	body   strings.Builder
	inData bool
}

func (s *smtpTestSession) writeLine(line string) bool {
	if _, err := s.bw.WriteString(line + "\r\n"); err != nil {
		return false
	}
	return s.bw.Flush() == nil
}

func (s *smtpTestSession) run() {
	if !s.writeLine("220 runwisp-test ESMTP ready") {
		return
	}
	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		var keep bool
		if s.inData {
			keep = s.handleDataLine(line)
		} else {
			keep = s.handleCommand(line)
		}
		if !keep {
			return
		}
	}
}

func (s *smtpTestSession) handleDataLine(line string) bool {
	if line == "." {
		s.inData = false
		select {
		case s.bodies <- s.body.String():
		default:
		}
		s.body.Reset()
		return s.writeLine("250 2.0.0 Ok: queued")
	}
	if strings.HasPrefix(line, "..") {
		line = line[1:]
	}
	s.body.WriteString(line)
	s.body.WriteString("\n")
	return true
}

func (s *smtpTestSession) handleCommand(line string) bool {
	upper := strings.ToUpper(line)
	switch {
	case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
		return s.writeLine("250-runwisp-test") && s.writeLine("250 SMTPUTF8")
	case strings.HasPrefix(upper, "MAIL FROM"),
		strings.HasPrefix(upper, "RCPT TO"),
		upper == "NOOP",
		upper == "RSET":
		return s.writeLine("250 2.1.0 Ok")
	case upper == "DATA":
		if !s.writeLine("354 End data with <CR><LF>.<CR><LF>") {
			return false
		}
		s.inData = true
		return true
	case upper == "QUIT":
		_ = s.writeLine("221 2.0.0 Bye")
		return false
	default:
		return s.writeLine("250 Ok")
	}
}

func (s *testSMTPServer) WaitForMessage(t *testing.T, timeout time.Duration) string {
	t.Helper()
	select {
	case b := <-s.bodies:
		return b
	case <-time.After(timeout):
		t.Fatalf("SMTP test server never received a DATA message within %s", timeout)
		return ""
	}
}

func (s *testSMTPServer) ExpectNoMessage(t *testing.T, window time.Duration) {
	t.Helper()
	select {
	case b := <-s.bodies:
		t.Fatalf("SMTP test server received an unexpected second message: %q", b)
	case <-time.After(window):
	}
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
) server.NotificationDTO {
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
			env, err := apiclient.DecodeNotificationEnvelope(ev.Data)
			require.NoError(t, err)
			return env.Notification
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
) server.NotificationsListBody {
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
	return server.NotificationsListBody{}
}

func hasDeliveryFailure(items []server.NotificationDTO) bool {
	for _, n := range items {
		if n.Kind == "notify.delivery_failed" {
			return true
		}
	}
	return false
}

// TestNotificationsUnreadCountShipsOnEveryEvent verifies the regression fix
// for SSE-driven badge drift: every notification SSE event carries the
// post-mutation unread count, and mark-all-read emits a count-only
// notification.unreadCountChanged event so listeners can refresh the
// badge without delta-tracking.
func TestNotificationsUnreadCountShipsOnEveryEvent(t *testing.T) {
	configPath := writeNotifyConfig(t, `
[tasks.fail-task]
run = "exit 1"
`)

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	events, err := client.StreamNotifications(streamCtx)
	require.NoError(t, err)
	waitForFirstPing(t, events)

	_, err = client.TriggerRun("fail-task", nil)
	require.NoError(t, err)

	createdEnv := waitForNotificationEnvelope(t, events, "notification.created", 10*time.Second)
	require.EqualValues(t, 1, createdEnv.UnreadCount,
		"notification.created must ship the post-mutation unread count")

	require.NoError(t, client.MarkNotificationRead(createdEnv.Notification.ID))
	updatedEnv := waitForNotificationEnvelope(t, events, "notification.updated", 5*time.Second)
	require.EqualValues(t, 0, updatedEnv.UnreadCount,
		"mark-read emits notification.updated with the fresh unread count")

	require.NoError(t, client.MarkNotificationUnread(createdEnv.Notification.ID))
	unreadEnv := waitForNotificationEnvelope(t, events, "notification.updated", 5*time.Second)
	require.EqualValues(t, 1, unreadEnv.UnreadCount)

	require.NoError(t, client.MarkAllNotificationsRead())
	count := waitForUnreadCountEvent(t, events, 5*time.Second)
	require.EqualValues(t, 0, count,
		"mark-all-read must emit notification.unreadCountChanged with 0")
}

func waitForNotificationEnvelope(
	t *testing.T,
	events <-chan apiclient.NotificationStreamEvent,
	wanted string,
	timeout time.Duration,
) server.NotificationCreatedEvent {
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
			env, err := apiclient.DecodeNotificationEnvelope(ev.Data)
			require.NoError(t, err)
			return env
		case <-deadline:
			t.Fatalf("never received SSE event %q", wanted)
			return server.NotificationCreatedEvent{}
		}
	}
}

func waitForUnreadCountEvent(
	t *testing.T,
	events <-chan apiclient.NotificationStreamEvent,
	timeout time.Duration,
) int64 {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("stream closed before notification.unreadCountChanged arrived")
			}
			if ev.Type != "notification.unreadCountChanged" {
				continue
			}
			count, err := apiclient.DecodeUnreadCountEnvelope(ev.Data)
			require.NoError(t, err)
			return count
		case <-deadline:
			t.Fatal("never received notification.unreadCountChanged")
			return 0
		}
	}
}
