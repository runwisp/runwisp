// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyDaemonLogLine(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		fatal  bool
		expect string
	}{
		{"fatal_uppercase", "FATA something broke", true, "FATA something broke"},
		{"fatal_error_phrase", "2026-01-01 Fatal error: oops", true, "2026-01-01 Fatal error: oops"},
		{"server_failed", "Server failed to bind", true, "Server failed to bind"},
		{"address_in_use", "listen tcp 127.0.0.1:9477: bind: address already in use", true, "listen tcp 127.0.0.1:9477: bind: address already in use"},
		{"bind_perm_denied", "bind: permission denied", true, "bind: permission denied"},
		{"benign_info", "INFO: daemon listening on :9477", false, ""},
		{"empty", "", false, ""},
		{
			"cli_error_badge",
			" ERROR  no runwisp.toml found at /tmp/x — create one to define your tasks",
			true,
			"no runwisp.toml found at /tmp/x — create one to define your tasks",
		},
		{"routine_slog_error_bracket", "[ERROR] notify delivery failed, retrying", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, fatal := classifyDaemonLogLine(tc.line)
			assert.Equal(t, tc.fatal, fatal)
			assert.Equal(t, tc.expect, msg)
		})
	}
}

func TestBindFailureHint_NoTail(t *testing.T) {
	assert.Nil(t, bindFailureHint("", "127.0.0.1", 9477))
}

func TestBindFailureHint_NotABindError(t *testing.T) {
	assert.Nil(t, bindFailureHint("something else went wrong", "127.0.0.1", 9477))
}

func TestBindFailureHint_AddressInUseReturnsUserFacing(t *testing.T) {
	err := bindFailureHint("listen tcp 0.0.0.0:9477: bind: address already in use", "0.0.0.0", 9477)
	require.Error(t, err)
	ufe, ok := isUserFacing(err)
	require.True(t, ok, "expected a *userFacingError")
	assert.Contains(t, ufe.title+ufe.details, "9477")
}

func TestTailFile_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.log")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	assert.Equal(t, "", tailFile(path, 4096))
}

func TestTailFile_SmallerThanLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.log")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o600))
	assert.Equal(t, "hello\n", tailFile(path, 4096))
}

func TestTailFile_LargerThanLimitReturnsTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")
	body := bytes.Repeat([]byte("X"), 5000)
	body = append(body, []byte("END")...)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	got := tailFile(path, 100)
	assert.Equal(t, 100, len(got))
	assert.True(t, strings.HasSuffix(got, "END"))
}

func TestTailFile_MissingReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", tailFile("/no/such/file.log", 1024))
}

func TestProcessAlive_NoPidFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "no-such-pid")
	assert.False(t, processAlive(os.Getpid(), pidPath))
}

func TestProcessAlive_PresentAndAlive(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	require.NoError(t, os.WriteFile(pidPath, []byte("dummy"), 0o600))
	// Use our own PID; we know it's alive.
	assert.True(t, processAlive(os.Getpid(), pidPath))
}

func TestProcessAlive_PresentButDead(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	require.NoError(t, os.WriteFile(pidPath, []byte("dummy"), 0o600))
	// PID 0 is the kernel scheduler placeholder on Linux; Signal(0) to it
	// fails with EINVAL/ESRCH, so processAlive reports false.
	assert.False(t, processAlive(0, pidPath))
}

func TestDaemonLogDrainer_NoFileNoFatal(t *testing.T) {
	dir := t.TempDir()
	d := &daemonLogDrainer{path: filepath.Join(dir, "missing")}
	defer d.close()
	msg, fatal := d.drain()
	assert.Equal(t, "", msg)
	assert.False(t, fatal)
}

func TestCheckPidAlive_NoPidFileConservativelyAlive(t *testing.T) {
	t.Parallel()
	// No PID file → ReadPidFile errors → checkPidAlive returns (0, true).
	dir := t.TempDir()
	pid, alive := checkPidAlive(filepath.Join(dir, "daemon.pid"), dir)
	assert.Equal(t, 0, pid)
	assert.True(t, alive, "missing PID file: stay conservative (assume alive)")
}

func TestCheckPidAlive_LivePid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600))

	pid, alive := checkPidAlive(pidPath, dir)
	assert.Equal(t, os.Getpid(), pid)
	assert.True(t, alive)
}

func TestCheckPidAlive_DeadPid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	// PID 0 is dead.
	require.NoError(t, os.WriteFile(pidPath, []byte("0"), 0o600))

	pid, alive := checkPidAlive(pidPath, dir)
	assert.Equal(t, 0, pid)
	assert.False(t, alive)
}

func TestPollHealth_TimesOut(t *testing.T) {
	c := apiclient.New("http://127.0.0.1:1", "")
	start := time.Now()
	err := pollHealth(c, 50*time.Millisecond)
	require.Error(t, err)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("pollHealth blew past timeout: %v", elapsed)
	}
}

func TestWaitForProcessExit_AlreadyDead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No PID file → processAlive() reports false immediately.
	err := waitForProcessExit(0, 500*time.Millisecond, dir)
	require.NoError(t, err)
}

func TestWaitForProcessExit_TimesOutOnLivePid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600))

	err := waitForProcessExit(os.Getpid(), 50*time.Millisecond, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not shut down")
}

func TestWaitForDaemonLoop_ReturnsNilOnSuccessfulHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, nil, 0o600))
	drainer := &daemonLogDrainer{path: logPath}
	defer drainer.close()

	pidPath := filepath.Join(dir, "daemon.pid")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	client := apiclient.New(srv.URL, "")
	err, timedOut := waitForDaemonLoop(client, drainer, pidPath, time.Now().Add(time.Second), ticker, dir)
	require.NoError(t, err)
	assert.False(t, timedOut)
}

func TestWaitForDaemonLoop_ReturnsFatalFromLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, []byte("Fatal error: bind failed\n"), 0o600))
	drainer := &daemonLogDrainer{path: logPath}
	defer drainer.close()

	pidPath := filepath.Join(dir, "daemon.pid")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	// 127.0.0.1:1 always refuses, so HealthCheck never succeeds.
	client := apiclient.New("http://127.0.0.1:1", "")
	err, timedOut := waitForDaemonLoop(client, drainer, pidPath, time.Now().Add(2*time.Second), ticker, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Fatal error")
	assert.False(t, timedOut)
}

// TestWaitForDaemonLoop_ReturnsFatalFromRenderedCLIErrorBadge reproduces the
// real daemon.log content a spawned `runwisp daemon` writes when
// loadDaemonConfig fails (e.g. no runwisp.toml): fang's handleCLIError renders
// an unbracketed "ERROR" badge and the process exits immediately. Before
// classifyDaemonLogLine recognized that badge, this fell through to the full
// health-check timeout instead of surfacing the real cause right away.
func TestWaitForDaemonLoop_ReturnsFatalFromRenderedCLIErrorBadge(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	rendered := " ERROR  no runwisp.toml found at " + dir + "\n\n" +
		"Create one to define your tasks:\n" +
		"  • See https://docs.runwisp.com/configuration/overview/ for the format\n" +
		"  • Or run `runwisp demo` to explore a fully-populated instance without writing one\n"
	require.NoError(t, os.WriteFile(logPath, []byte(rendered), 0o600))
	drainer := &daemonLogDrainer{path: logPath}
	defer drainer.close()

	pidPath := filepath.Join(dir, "daemon.pid")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	client := apiclient.New("http://127.0.0.1:1", "")
	start := time.Now()
	err, timedOut := waitForDaemonLoop(client, drainer, pidPath, time.Now().Add(10*time.Second), ticker, dir)
	require.Error(t, err)
	// The badge is stripped (the caller re-renders through the same "ERROR"
	// badge machinery, so keeping it would print two badges back to back),
	// but the rest of the operator's guidance survives intact.
	assert.NotContains(t, err.Error(), "ERROR")
	assert.Contains(t, err.Error(), "no runwisp.toml found at "+dir)
	assert.Contains(t, err.Error(), "runwisp demo")
	assert.False(t, timedOut)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected a fast fail, took %v", elapsed)
	}
}

func TestWaitForDaemonLoop_TimesOut(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, nil, 0o600))
	drainer := &daemonLogDrainer{path: logPath}
	defer drainer.close()

	pidPath := filepath.Join(dir, "daemon.pid")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	client := apiclient.New("http://127.0.0.1:1", "")
	err, timedOut := waitForDaemonLoop(client, drainer, pidPath, time.Now().Add(50*time.Millisecond), ticker, dir)
	require.Error(t, err)
	assert.True(t, timedOut)
	assert.Contains(t, err.Error(), "timed out")
}

func TestWaitForDaemonLoop_PidDisappearance(t *testing.T) {
	dir := t.TempDir()

	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, nil, 0o600))
	drainer := &daemonLogDrainer{path: logPath}
	defer drainer.close()

	pidPath := filepath.Join(dir, "daemon.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600))

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	// Schedule PID-file removal so the loop observes pidSeen→gone.
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.Remove(pidPath)
	}()

	client := apiclient.New("http://127.0.0.1:1", "")
	err, timedOut := waitForDaemonLoop(client, drainer, pidPath, time.Now().Add(2*time.Second), ticker, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited")
	assert.False(t, timedOut)
}

func TestWaitForDaemon_SurfacesEmptyLogTailNote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, nil, 0o600))

	client := apiclient.New("http://127.0.0.1:1", "")
	err := waitForDaemon(client, logPath, 50*time.Millisecond, Flags{DataDir: dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestWaitForDaemon_SuccessDoesNotDumpLogTail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, []byte("[INFO] ready, listening\n"), 0o600))

	// Capture stderr: the drainer already streams lines live, so a successful
	// startup must not repeat them in a "--- daemon log ---" tail block.
	orig := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = buf.ReadFrom(r); close(done) }()

	client := apiclient.New(srv.URL, "")
	waitErr := waitForDaemon(client, logPath, time.Second, Flags{DataDir: dir})

	w.Close()
	os.Stderr = orig
	<-done

	require.NoError(t, waitErr)
	assert.NotContains(t, buf.String(), "--- daemon log")
}

func TestWaitForDaemon_PromotesBindFailureHint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, []byte("listen tcp 0.0.0.0:9477: bind: address already in use\n"), 0o600))

	client := apiclient.New("http://127.0.0.1:1", "")
	err := waitForDaemon(client, logPath, 50*time.Millisecond, Flags{DataDir: dir})
	require.Error(t, err)
	_, ok := isUserFacing(err)
	assert.True(t, ok, "bind-failure must surface as a userFacingError")
}

func TestShutdownDaemon_MissingPidFile(t *testing.T) {
	t.Parallel()
	err := shutdownDaemon(Flags{DataDir: t.TempDir()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PID file")
}

func TestShutdownDaemon_PidFromInvalidContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Write garbage to the daemon PID file: ReadPidFile parses with strconv,
	// so non-numeric content surfaces as a parse error.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte("not-a-number"), 0o600))

	err := shutdownDaemon(Flags{DataDir: dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PID file")
}

func TestPollHealth_HitsServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := apiclient.New(srv.URL, "")
	require.NoError(t, pollHealth(c, time.Second))
}

func TestDaemonLogDrainer_AppendedLinesDetectFatal(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "daemon.log")
	require.NoError(t, os.WriteFile(logPath, []byte("INFO ok\n"), 0o600))

	d := &daemonLogDrainer{path: logPath}
	defer d.close()

	msg, fatal := d.drain()
	assert.False(t, fatal)
	assert.Equal(t, "", msg)

	// Append a fatal line; drain should pick it up on next call.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, _ = f.WriteString("Fatal error: boom\n")
	require.NoError(t, f.Close())

	msg, fatal = d.drain()
	assert.True(t, fatal)
	assert.Contains(t, msg, "Fatal error: boom")
}
