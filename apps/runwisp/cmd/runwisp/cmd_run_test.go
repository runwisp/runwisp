// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
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
	origHost := flags.Host
	flags.Host = "127.0.0.1"
	t.Cleanup(func() { flags.Host = origHost })

	out := captureSlog(t, func() {
		cfg := &daemonConfig{Config: &config.Config{}}
		cfg.Config.Daemon.AllowCloudDispatch = true
		logSecurityWarnings(cfg)
	})
	assert.Contains(t, out, "Cloud shell dispatch enabled")
}

func TestLogSecurityWarnings_PrintsBannerForNonLoopbackHost(t *testing.T) {
	origHost := flags.Host
	flags.Host = "0.0.0.0"
	t.Cleanup(func() { flags.Host = origHost })

	out := captureStderr(t, func() {
		cfg := &daemonConfig{Config: &config.Config{}}
		logSecurityWarnings(cfg)
	})
	assert.True(t, strings.Contains(out, "0.0.0.0"), "banner must include the host")
}

func TestLogSecurityWarnings_LoopbackQuiet(t *testing.T) {
	origHost := flags.Host
	flags.Host = "127.0.0.1"
	t.Cleanup(func() { flags.Host = origHost })

	out := captureStderr(t, func() {
		cfg := &daemonConfig{Config: &config.Config{}}
		logSecurityWarnings(cfg)
	})
	assert.NotContains(t, out, "SECURITY", "loopback bind must not print the banner")
}

// pickFreePort grabs an ephemeral port and immediately releases it so the
// daemon can bind. Inherently racy but acceptable for serial tests.
func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

func snapshotFlags(t *testing.T) {
	t.Helper()
	o := flags
	t.Cleanup(func() { flags = o })
}

func TestRunDaemon_BadDBPathReturnsError(t *testing.T) {
	snapshotFlags(t)
	// Point flags.DataDir at a directory we can't write into so DBPath()
	// resolves under it and storage.New fails.
	flags.DataDir = "/proc/runwisp-cannot-create"
	flags.CfgFile = writeMinimalTOML(t)
	noTUI = true
	t.Cleanup(func() { noTUI = false })

	err := runDaemon(modeStandalone)
	require.Error(t, err)
}

func TestRunDaemon_MissingConfigReturnsError(t *testing.T) {
	snapshotFlags(t)
	dir := t.TempDir()
	flags.DataDir = dir
	flags.CfgFile = "/no/such/runwisp.toml"
	flags.Host = "127.0.0.1"
	flags.Port = pickFreePort(t)
	noTUI = true
	t.Cleanup(func() { noTUI = false })
	t.Setenv("RUNWISP_PASSWORD", "x")

	err := runDaemon(modeStandalone)
	require.Error(t, err)
}

func TestRunDaemon_BootsAndShutsDownOnSIGTERM(t *testing.T) {
	snapshotFlags(t)
	// ShortTempDir keeps DataDir path under macOS' 104-byte sun_path limit so
	// the daemon's runwisp.sock can actually bind. t.TempDir embeds the test
	// name, blowing past the limit and failing with EINVAL.
	dir := testutil.ShortTempDir(t)
	flags.DataDir = dir
	flags.CfgFile = writeMinimalTOML(t)
	flags.Host = "127.0.0.1"
	flags.Port = pickFreePort(t)
	noTUI = true
	t.Cleanup(func() { noTUI = false })

	t.Setenv("RUNWISP_PASSWORD", "test-password")

	done := make(chan error, 1)
	go func() {
		done <- runDaemon(modeStandalone)
	}()

	// Poll the bound HTTP port until the daemon is up. Server.Start does its
	// own background bind; we treat a successful TCP connect as ready.
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(flags.Host, strconv.Itoa(flags.Port)), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, ready, "daemon never bound %d", flags.Port)

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
	_, err = http.Get("http://" + net.JoinHostPort(flags.Host, strconv.Itoa(flags.Port)) + "/health")
	assert.Error(t, err, "port should be free after shutdown")
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
	prev := noTUI
	noTUI = true
	t.Cleanup(func() { noTUI = prev })

	buf := server.NewDaemonLogBuffer(8)
	assert.Nil(t, configureBootLogRouting(buf))
}

func TestConfigureBootLogRouting_TUIReturnsNonNilWriter(t *testing.T) {
	prev := noTUI
	noTUI = false
	t.Cleanup(func() { noTUI = prev })

	buf := server.NewDaemonLogBuffer(8)
	dw := configureBootLogRouting(buf)
	assert.NotNil(t, dw)
}

func TestStartCloudIfEnabled_StandaloneReturnsNoOps(t *testing.T) {
	cancel, wg := startCloudIfEnabled(modeStandalone, nil, nil)
	require.NotNil(t, cancel)
	require.NotNil(t, wg)
	cancel() // must not panic
}

func TestStartCloudIfEnabled_CloudWithDisabledConfigShortCircuits(t *testing.T) {
	cfg := &daemonConfig{CloudConfig: cloud.Config{Enabled: false}}
	cancel, wg := startCloudIfEnabled(modeCloud, cfg, nil)
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		superviseServerStart(srv)
	}()

	select {
	case sig := <-sigCh:
		assert.Equal(t, syscall.SIGTERM, sig)
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not self-signal SIGTERM")
	}
	<-done
}

func TestEmitStartupBanner_DoesNotPanic(t *testing.T) {
	// Cover both branches: the fancy banner path runs when FancyBanner is
	// true (TTY + non-JSON), and logStartupSummary runs otherwise.
	info := uikit.StartupInfo{Version: "test", Tasks: nil}

	prevFormat := flags.LogFormat
	t.Cleanup(func() { flags.LogFormat = prevFormat })

	flags.LogFormat = clilog.FormatJSON // forces non-fancy branch
	emitStartupBanner(info)

	flags.LogFormat = clilog.FormatText // whichever stderrTTY says
	emitStartupBanner(info)
}
