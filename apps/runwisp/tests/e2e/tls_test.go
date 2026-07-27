//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// mapPinStore is an in-memory CertPinStore for tests: TOFU pins live only for
// the lifetime of the test, so each test starts from a clean trust slate.
type mapPinStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newMapPinStore() *mapPinStore { return &mapPinStore{m: map[string]string{}} }

func (s *mapPinStore) Load(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok
}

func (s *mapPinStore) Store(k, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
}

// startTLSDaemon boots a daemon bound to 0.0.0.0, which triggers auto-HTTPS
// (loopback would stay plain HTTP). It returns the daemon process handle and
// the https base URL reachable over loopback — the self-signed cert's SANs
// include 127.0.0.1, and pinning skips hostname verification anyway.
func startTLSDaemon(t *testing.T) *daemonProcess {
	t.Helper()
	return startNonLoopbackDaemon(t, writeE2EConfig(t, t.TempDir()), "https")
}

// startNonLoopbackDaemon boots a daemon bound to 0.0.0.0 with the given config
// and returns its process handle. scheme selects the base URL the returned
// handle advertises ("http" or "https"); callers pick it to match what the
// config makes the daemon serve.
func startNonLoopbackDaemon(t *testing.T, configPath, scheme string) *daemonProcess {
	t.Helper()

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	dataDir := testutil.ShortTempDir(t)
	port := reserveTCPPort(t)

	output := &lockedBuffer{}
	cmd := exec.Command(
		binaryPath,
		"--config", configPath,
		"--data", dataDir,
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(port),
		"daemon",
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv("TERM=xterm-256color")
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	require.NoError(t, cmd.Start())

	process := &daemonProcess{
		baseURL:  scheme + "://127.0.0.1:" + strconv.Itoa(port),
		dataDir:  dataDir,
		port:     port,
		cmd:      cmd,
		output:   output,
		waitDone: make(chan struct{}),
	}
	go func() {
		process.waitErr = cmd.Wait()
		close(process.waitDone)
	}()
	t.Cleanup(func() { process.stop(t) })

	return process
}

func TestTLS_AutoHTTPSOnNonLoopbackBind(t *testing.T) {
	d := startTLSDaemon(t)

	// Connect over HTTPS with a pinning client. The first successful connect is
	// also the trust-on-first-use moment: the cert fingerprint gets recorded.
	store := newMapPinStore()
	client := apiclient.NewPinned(d.baseURL, "", store)
	d.waitForReady(t, client, 15*time.Second)

	pinned, ok := store.Load(apiclient.NormalizeBaseURL(d.baseURL))
	require.True(t, ok, "cert should be pinned on first connect (TOFU)")
	require.NotEmpty(t, pinned)

	// The startup banner discloses the same fingerprint the client pinned, so an
	// operator can verify it out-of-band.
	requireOutputContains(t, d, 5*time.Second, "Serving HTTPS")
	requireOutputContains(t, d, 5*time.Second, "sha256:"+pinned)

	// HSTS is present on a TLS response (and the handshake itself proves the
	// daemon served real HTTPS).
	rawTLS := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // test asserts header presence, not chain trust
	}
	resp, err := rawTLS.Get(d.baseURL + "/health")
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Strict-Transport-Security"), "HSTS must be set over TLS")

	// Plain HTTP to the TLS port must not be served as HTTP. Go's TLS server
	// answers a cleartext request with a 400 ("Client sent an HTTP request to
	// an HTTPS server"), so assert that rather than a transport error.
	httpResp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://127.0.0.1:" + strconv.Itoa(d.port) + "/health")
	require.NoError(t, err)
	defer httpResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, httpResp.StatusCode, "TLS port must reject plain HTTP, not serve it")
	body, _ := io.ReadAll(httpResp.Body)
	require.Contains(t, string(body), "HTTPS", "the TLS port should tell a plain-HTTP client it speaks HTTPS")
}

func TestTLS_PinMismatchFailsLoudly(t *testing.T) {
	d := startTLSDaemon(t)

	// First, learn the daemon's real fingerprint via an honest TOFU connect.
	store := newMapPinStore()
	d.waitForReady(t, apiclient.NewPinned(d.baseURL, "", store), 15*time.Second)
	key := apiclient.NormalizeBaseURL(d.baseURL)
	_, ok := store.Load(key)
	require.True(t, ok)

	// Now pretend a different cert was pinned earlier (cert rotated, or an
	// interceptor). The next connect must fail with a mismatch, not silently
	// trust the new cert.
	poisoned := newMapPinStore()
	poisoned.Store(key, "00deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbe")
	client := apiclient.NewPinned(d.baseURL, "", poisoned)

	err := client.HealthCheck()
	require.Error(t, err, "a changed cert must fail the handshake")
	require.Contains(t, err.Error(), "changed", "error should read like a known-hosts mismatch")
}

func TestTLS_OffKeepsHTTPWithLoudBanner(t *testing.T) {
	// tls = "off" opts out of auto-HTTPS even on a non-loopback bind. The
	// daemon then serves cleartext and must warn loudly that it is doing so.
	configPath := filepath.Join(t.TempDir(), "runwisp.tls-off.toml")
	config := `
[daemon]
tls = "off"
shutdown_timeout = "500ms"

[tasks.noop]
run = "true"
`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))

	d := startNonLoopbackDaemon(t, configPath, "http")

	// Plain HTTP must work — no TLS handshake involved.
	d.waitForReady(t, apiclient.New(d.baseURL, ""), 15*time.Second)

	// And the operator must be told the channel is unencrypted.
	requireOutputContains(t, d, 5*time.Second, "cleartext")
}

// requireOutputContains polls the daemon's combined output until it contains
// want, failing the test (with the captured output) if the deadline passes.
func requireOutputContains(t *testing.T, d *daemonProcess, timeout time.Duration, want string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(d.output.Tail(64_000), want) {
			return
		}
		time.Sleep(screenPollInterval)
	}
	require.Failf(t, "daemon output missing expected text", "want %q\noutput:\n%s", want, d.output.Tail(64_000))
}
