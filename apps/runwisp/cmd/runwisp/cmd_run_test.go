// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/clilog"
	"github.com/runwisp/runwisp/internal/cloud"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything written. Tests use it to inspect non-loopback banners.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&buf, r)
	}()

	fn()

	require.NoError(t, w.Close())
	wg.Wait()
	os.Stderr = old
	return buf.String()
}

// captureSlog redirects the slog default logger to an in-memory buffer for
// the duration of fn.
func captureSlog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	fn()
	return buf.String()
}

func TestPrintNonLoopbackBanner_MentionsHost(t *testing.T) {
	out := captureStderr(t, func() { printNonLoopbackBanner("10.0.0.5") })
	assert.Contains(t, out, "10.0.0.5")
	assert.Contains(t, out, "SECURITY")
}

func TestLogSecurityWarnings_EmitsCloudDispatchWarning(t *testing.T) {
	out := captureSlog(t, func() {
		cfg := &daemonConfig{Config: &config.Config{}}
		cfg.Config.Daemon.AllowCloudDispatch = true
		logSecurityWarnings(cfg, Flags{Host: "127.0.0.1"}, tlsSetup{Scheme: "http"})
	})
	assert.Contains(t, out, "Cloud dispatch enabled")
}

func TestLogSecurityWarnings_PrintsBannerForNonLoopbackCleartext(t *testing.T) {
	// Non-loopback bind with TLS off (scheme http) is the only case that still
	// prints the loud cleartext-exposure banner.
	out := captureStderr(t, func() {
		cfg := &daemonConfig{Config: &config.Config{}}
		logSecurityWarnings(cfg, Flags{Host: "0.0.0.0"}, tlsSetup{Scheme: "http"})
	})
	assert.True(t, strings.Contains(out, "0.0.0.0"), "banner must include the host")
	assert.Contains(t, out, "SECURITY")
}

func TestLogSecurityWarnings_NonLoopbackHTTPSNoCleartextBanner(t *testing.T) {
	// Auto-HTTPS removes the eavesdrop risk, so a non-loopback bind serving
	// HTTPS gets a calm fingerprint line, not the cleartext banner.
	stderr := captureStderr(t, func() {
		cfg := &daemonConfig{Config: &config.Config{}}
		logSecurityWarnings(cfg, Flags{Host: "0.0.0.0"}, tlsSetup{Scheme: "https", Generated: true, Fingerprint: "deadbeef"})
	})
	assert.NotContains(t, stderr, "cleartext", "HTTPS bind must not print the cleartext banner")

	slogOut := captureSlog(t, func() {
		cfg := &daemonConfig{Config: &config.Config{}}
		logSecurityWarnings(cfg, Flags{Host: "0.0.0.0"}, tlsSetup{Scheme: "https", Generated: true, Fingerprint: "deadbeef"})
	})
	assert.Contains(t, slogOut, "Serving HTTPS")
	assert.Contains(t, slogOut, "deadbeef")
}

func TestLogSecurityWarnings_LoopbackQuiet(t *testing.T) {
	out := captureStderr(t, func() {
		cfg := &daemonConfig{Config: &config.Config{}}
		logSecurityWarnings(cfg, Flags{Host: "127.0.0.1"}, tlsSetup{Scheme: "http"})
	})
	assert.NotContains(t, out, "SECURITY", "loopback bind must not print the banner")
}

func TestRunDaemon_BadDBPathReturnsError(t *testing.T) {
	// Point DataDir at a directory we can't write into so DBPath()
	// resolves under it and storage.New fails.
	f := Flags{
		DataDir: "/proc/runwisp-cannot-create",
		CfgFile: writeMinimalTOML(t),
	}
	err := runDaemon(modeStandalone, f, true)
	require.Error(t, err)
}

func TestRunDaemon_MissingConfigReturnsError(t *testing.T) {
	f := Flags{
		DataDir: t.TempDir(),
		CfgFile: "/no/such/runwisp.toml",
		Host:    "127.0.0.1",
		Port:    testutil.PickFreePort(t),
	}
	t.Setenv("RUNWISP_PASSWORD", "x")

	err := runDaemon(modeStandalone, f, true)
	require.Error(t, err)
}

func TestRunDaemon_BootsAndShutsDownOnSIGTERM(t *testing.T) {
	if testing.Short() {
		// Boots a full daemon and sends a real SIGTERM — too heavy for the
		// local fast loop. CI never passes -short, so this still runs there.
		t.Skip("skipping full daemon boot/SIGTERM test in -short mode")
	}
	// ShortTempDir keeps DataDir path under macOS' 104-byte sun_path limit so
	// the daemon's runwisp.sock can actually bind. t.TempDir embeds the test
	// name, blowing past the limit and failing with EINVAL.
	dir := testutil.ShortTempDir(t)
	f := Flags{
		DataDir: dir,
		CfgFile: writeMinimalTOML(t),
		Host:    "127.0.0.1",
		Port:    testutil.PickFreePort(t),
	}

	t.Setenv("RUNWISP_PASSWORD", "test-password")

	done := make(chan error, 1)
	go func() {
		done <- runDaemon(modeStandalone, f, true)
	}()

	// Poll the bound HTTP port until the daemon is up. Server.Start does its
	// own background bind; we treat a successful TCP connect as ready.
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(f.Host, strconv.Itoa(f.Port)), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, ready, "daemon never bound %d", f.Port)

	// SIGTERM the test process; runHeadless installed the handler.
	require.NoError(t, sendSelfSIGTERM())

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("runDaemon did not return after SIGTERM")
	}

	// The daemon should have cleaned up its PID file.
	_, err := os.Stat(dir + "/runwisp.pid")
	assert.True(t, os.IsNotExist(err), "PID file should be removed on clean shutdown")

	// Smoke-test that the listener was actually released.
	_, err = http.Get("http://" + net.JoinHostPort(f.Host, strconv.Itoa(f.Port)) + "/health")
	assert.Error(t, err, "port should be free after shutdown")
}

// TestRunDaemon_AutoHeadlessWithoutTerminal verifies the no-TTY fallback: when
// runDaemon is asked for the TUI (headless=false) but stdin/stdout are not a
// terminal (as under the test harness, systemd, or Docker), it must auto-disable
// the TUI and boot headless instead of blocking forever on the TUI's stdin.
func TestRunDaemon_AutoHeadlessWithoutTerminal(t *testing.T) {
	if testing.Short() {
		// Boots a full daemon and sends a real SIGTERM — too heavy for the
		// local fast loop. CI never passes -short, so this still runs there.
		t.Skip("skipping full daemon boot/SIGTERM test in -short mode")
	}
	require.False(t, isInteractiveTerminal(), "test harness must be non-interactive for this to be meaningful")

	dir := testutil.ShortTempDir(t)
	f := Flags{
		DataDir: dir,
		CfgFile: writeMinimalTOML(t),
		Host:    "127.0.0.1",
		Port:    testutil.PickFreePort(t),
	}

	t.Setenv("RUNWISP_PASSWORD", "test-password")

	done := make(chan error, 1)
	go func() {
		// headless=false asks for the TUI, but with no interactive terminal
		// runDaemon must flip to headless rather than block attaching it.
		done <- runDaemon(modeStandalone, f, false)
	}()

	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(f.Host, strconv.Itoa(f.Port)), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, ready, "daemon never bound %d — likely blocked attaching the TUI", f.Port)

	require.NoError(t, sendSelfSIGTERM())

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("runDaemon did not return after SIGTERM")
	}
}

func TestInstallSignalHandler_DeliversAndStops(t *testing.T) {
	sigCh, stop := installSignalHandler()
	defer stop()

	p, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, p.Signal(syscall.SIGTERM))

	select {
	case sig := <-sigCh:
		assert.Equal(t, syscall.SIGTERM, sig)
	case <-time.After(time.Second):
		t.Fatal("signal not delivered to channel")
	}
}

func TestConfigureBootLogRouting_HeadlessReturnsNilWriter(t *testing.T) {
	buf := server.NewDaemonLogBuffer(8)
	assert.Nil(t, configureBootLogRouting(buf, Flags{}, true))
}

func TestConfigureBootLogRouting_TUIReturnsNonNilWriter(t *testing.T) {
	buf := server.NewDaemonLogBuffer(8)
	dw := configureBootLogRouting(buf, Flags{}, false)
	assert.NotNil(t, dw)
}

func TestStartCloudIfEnabled_StandaloneReturnsNoOps(t *testing.T) {
	t.Parallel()
	cancel, wg := startCloudIfEnabled(modeStandalone, nil, nil, nil)
	require.NotNil(t, cancel)
	require.NotNil(t, wg)
	cancel() // must not panic
}

func TestStartCloudIfEnabled_CloudWithDisabledConfigShortCircuits(t *testing.T) {
	t.Parallel()
	cfg := &daemonConfig{CloudConfig: cloud.Config{Enabled: false}}
	cancel, wg := startCloudIfEnabled(modeCloud, cfg, nil, nil)
	require.NotNil(t, cancel)
	require.NotNil(t, wg)
	cancel()
	wg.Wait() // empty, returns immediately
}

func TestSuperviseServerStart_SelfSignalsOnStartError(t *testing.T) {
	// Run with our own installed signal handler so the supervisor's
	// self-SIGTERM is caught here instead of killing the test process — the
	// daemon's goroutine in production does the same because runDaemon
	// installed signal.Notify first.
	sigCh, stop := installSignalHandler()
	defer stop()

	// Empty SocketPath makes openUnixListener fail, which makes Start return
	// an error and the supervisor self-signal SIGTERM.
	srv, err := server.New(server.Options{
		Password:   "x",
		JWTSecret:  "test-secret-test-secret-test-1234",
		SocketPath: "",
	})
	require.NoError(t, err)

	fatalCh := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		superviseServerStart(srv, fatalCh)
	}()

	select {
	case sig := <-sigCh:
		assert.Equal(t, syscall.SIGTERM, sig)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not self-signal SIGTERM")
	}
	<-done

	// The fatal cause must be reported on fatalCh before the self-signal, so the
	// shutdown path can log it instead of a phantom external signal.
	select {
	case err := <-fatalCh:
		assert.Error(t, err)
	default:
		t.Fatal("supervisor did not report the fatal error on fatalCh")
	}
}

func TestReadFatal(t *testing.T) {
	// Empty channel → external signal, no fatal cause.
	assert.NoError(t, readFatal(make(chan error, 1)))
	// nil channel is treated as no fatal cause.
	assert.NoError(t, readFatal(nil))
	// A reported fatal error is surfaced so shutdown logs the real cause.
	ch := make(chan error, 1)
	ch <- errors.New("server failed")
	assert.Error(t, readFatal(ch))
}

func TestEmitStartupBanner_DoesNotPanic(t *testing.T) {
	// Cover both branches: the fancy banner path runs when FancyBanner is
	// true (TTY + non-JSON), and logStartupSummary runs otherwise.
	info := uikit.StartupInfo{Version: "test", Tasks: nil}

	emitStartupBanner(info, Flags{LogFormat: clilog.FormatJSON}) // forces non-fancy branch
	emitStartupBanner(info, Flags{LogFormat: clilog.FormatText}) // whichever stderrTTY says
}
