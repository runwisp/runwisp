//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

const (
	// 138 cols ⇒ main pane (cols − SidebarWidth 28) == uikit.MaxContentWidth 110,
	// so the exec table fills the pane exactly. At a wider 140 the table caps at
	// 110 and leaves a 2-col gap (the orphaned scrollbar in the docs screenshots).
	screenCols         = 138
	screenRows         = 40
	screenPollInterval = 50 * time.Millisecond
	processExitTimeout = 5 * time.Second
)

type tuiSuite struct {
	configPath string
	daemon     *daemonProcess
	tui        *tuiSession
}

func newTUISuite(t *testing.T) *tuiSuite {
	t.Helper()

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	tui := startRemoteTUI(t, projectDir, binaryPath, configPath, daemon)

	return &tuiSuite{
		configPath: configPath,
		daemon:     daemon,
		tui:        tui,
	}
}

func runwispProjectDir(t testing.TB) string {
	t.Helper()

	projectDir, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	return projectDir
}

var (
	appBinaryOnce sync.Once
	appBinaryPath string
)

func buildRunwispBinary(t testing.TB, projectDir string) string {
	t.Helper()

	appBinaryOnce.Do(func() {
		buildDir, err := os.MkdirTemp("", "runwisp-e2e-")
		require.NoError(t, err)
		appBinaryPath = filepath.Join(buildDir, "runwisp")
		buildCmd := exec.Command("go", "build", "-cover", "-o", appBinaryPath, "./cmd/runwisp")
		buildCmd.Dir = projectDir

		buildOutput, err := buildCmd.CombinedOutput()
		require.NoErrorf(t, err, "build RunWisp binary: %s", string(buildOutput))
	})

	return appBinaryPath
}

func (s *tuiSuite) client(t *testing.T) *apiclient.Client {
	t.Helper()

	return socketClient(t, s.daemon.dataDir)
}

// socketClient builds a Unix-socket apiclient pointed at the daemon's data
// dir, waiting briefly for the socket file to appear so it works across the
// race window between daemon start and listener bind. No password or
// authentication step is needed: the daemon trusts local-socket peers.
func socketClient(t testing.TB, dataDir string) *apiclient.Client {
	t.Helper()

	socketPath := filepath.Join(dataDir, "runwisp.sock")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(screenPollInterval)
	}
	require.FileExists(t, socketPath, "runwisp.sock should be created by the daemon")
	return apiclient.NewUnix(socketPath)
}

func (s *tuiSuite) selectAlphaTask(t *testing.T) string {
	t.Helper()

	s.tui.press(t, keyDown, keyEnter)
	return s.tui.waitForAll(t, 5*time.Second,
		"alpha-stream",
		"Schedule: manual",
		"Run Now (r)",
	)
}

func (s *tuiSuite) selectBravoTask(t *testing.T) string {
	t.Helper()

	s.tui.press(t, keyDown, keyDown, keyEnter)
	return s.tui.waitForAll(t, 5*time.Second,
		"bravo-fail",
		"Schedule: manual",
		"Run Now (r)",
	)
}

func (s *tuiSuite) selectInfoScreen(t *testing.T) string {
	t.Helper()

	// Info is the third sidebar item; cursor is on alpha after selectAlphaTask.
	s.tui.press(t, keyDown, keyDown, keyEnter)
	return s.tui.waitForAll(t, 5*time.Second, "System", "Configuration", "Tasks (2)")
}

func (s *tuiSuite) selectDebugScreen(t *testing.T) string {
	t.Helper()

	// Debug log is the item immediately below Info in the sidebar. We assert
	// on a stable INFO-level startup line (rather than e.g. an HTTP access
	// path) because the headless daemon emits HTTP access at DEBUG by
	// default — the Debug panel still receives daemon diagnostics at INFO,
	// which is what "Internal events and diagnostics" promises.
	s.tui.press(t, keyDown, keyEnter)
	return s.tui.waitForAll(t, 5*time.Second,
		"Debug Log",
		"Internal events and diagnostics",
		"RunWisp starting",
	)
}

func writeE2EConfig(t *testing.T, dir string) string {
	t.Helper()

	configPath := filepath.Join(dir, "runwisp.e2e.toml")
	config := `
[daemon]
shutdown_timeout = "500ms"

[tasks.alpha-stream]
run = """
set -eu
echo "alpha-line-1"
sleep 1
echo "alpha-line-2"
sleep 1
echo "alpha-line-3"
"""

[tasks.bravo-fail]
run = """
set -eu
echo "bravo-line-1"
sleep 1
>&2 echo "bravo-line-2"
exit 1
"""
`

	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))

	return configPath
}

type tuiSession struct {
	cmd      *exec.Cmd
	ptyFile  *os.File
	term     vt10x.Terminal
	output   *lockedBuffer
	waitDone chan struct{}
	waitErr  error
	stopOnce sync.Once

	repliedBackground bool
	repliedCursor     bool
}

type daemonProcess struct {
	baseURL string
	dataDir string
	port    int

	cmd      *exec.Cmd
	output   *lockedBuffer
	waitDone chan struct{}
	waitErr  error
	stopOnce sync.Once
}

func startDaemon(t *testing.T, projectDir, binaryPath, configPath string) *daemonProcess {
	t.Helper()

	return startDaemonOn(t, projectDir, binaryPath, configPath, testutil.ShortTempDir(t), reserveTCPPort(t))
}

// startDaemonOn boots a daemon against a caller-chosen data dir and port. Tests
// that must survive a daemon restart (or pre-seed the data dir before first
// boot) own the dir/port and reuse them across boots; startDaemon is the
// convenience wrapper that picks a throwaway dir and a free port.
func startDaemonOn(t *testing.T, projectDir, binaryPath, configPath, dataDir string, port int) *daemonProcess {
	t.Helper()

	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)

	output := &lockedBuffer{}
	cmd := exec.Command(
		binaryPath,
		"--config", configPath,
		"--data", dataDir,
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
		baseURL:  baseURL,
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

	t.Cleanup(func() {
		process.stop(t)
	})

	client := apiclient.New(baseURL, "")
	process.waitForReady(t, client, 10*time.Second)

	return process
}

func (d *daemonProcess) stop(t testing.TB) {
	t.Helper()

	d.stopOnce.Do(func() {
		if d.exited() {
			return
		}

		_ = killProcessGroup(d.cmd.Process.Pid, syscall.SIGINT)
		if d.waitForExit(processExitTimeout) {
			return
		}

		_ = killProcessGroup(d.cmd.Process.Pid, syscall.SIGKILL)
		d.waitForExit(processExitTimeout)
	})
}

func (d *daemonProcess) exited() bool {
	select {
	case <-d.waitDone:
		return true
	default:
		return false
	}
}

func (d *daemonProcess) waitForExit(timeout time.Duration) bool {
	select {
	case <-d.waitDone:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (d *daemonProcess) waitForReady(t testing.TB, client *apiclient.Client, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if client.HealthCheck() == nil {
			return
		}
		if d.exited() {
			require.FailNowf(t, "daemon exited early", "daemon output:\n%s", d.output.Tail(16_000))
		}
		time.Sleep(screenPollInterval)
	}

	require.FailNowf(t, "daemon did not become healthy", "daemon output:\n%s", d.output.Tail(16_000))
}

func startRemoteTUI(t *testing.T, projectDir, binaryPath, configPath string, daemon *daemonProcess) *tuiSession {
	t.Helper()

	return startRemoteTUIEnv(t, projectDir, binaryPath, configPath, daemon, "TERM=dumb")
}

// startRemoteTUIEnv is startRemoteTUI with caller-chosen extra env. The TUI
// screenshot capture passes a colour-capable TERM so lipgloss emits real SGR
// (the default "TERM=dumb" is enough for text assertions but strips colour).
func startRemoteTUIEnv(t *testing.T, projectDir, binaryPath, configPath string, daemon *daemonProcess, extraEnv ...string) *tuiSession {
	t.Helper()

	cmd := exec.Command(
		binaryPath,
		"--config", configPath,
		"--data", daemon.dataDir,
		"--port", strconv.Itoa(daemon.port),
		"tui",
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv(extraEnv...)

	session := launchTUISession(t, cmd)
	session.waitForAll(t, 10*time.Second,
		"Home",
		"Web UI",
		"Open Web UI",
	)

	return session
}

// startHTTPTUI launches a TUI that connects to the daemon purely over HTTP
// (--url), against a FRESH empty data dir that holds no socket — so the only
// way it can reach the daemon is the HTTP transport. extraEnv carries the auth
// inputs (e.g. RUNWISP_PASSWORD). Unlike startRemoteTUI it does not wait for a
// specific screen: auth and no-auth paths land on different first frames, so
// callers assert what they expect.
func startHTTPTUI(t *testing.T, projectDir, binaryPath, configPath string, daemon *daemonProcess, extraEnv ...string) *tuiSession {
	t.Helper()

	cmd := exec.Command(
		binaryPath,
		"--config", configPath,
		"--data", testutil.ShortTempDir(t),
		"--url", daemon.baseURL,
		"tui",
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv(append([]string{"TERM=dumb"}, extraEnv...)...)

	return launchTUISession(t, cmd)
}

// launchTUISession starts cmd under a PTY, wires a vt10x virtual terminal to
// capture rendered output, answers terminal capability probes, and registers
// cleanup. Shared by the socket and HTTP TUI launchers.
func launchTUISession(t *testing.T, cmd *exec.Cmd) *tuiSession {
	t.Helper()

	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: screenCols,
		Rows: screenRows,
	})
	require.NoError(t, err)

	session := &tuiSession{
		cmd:      cmd,
		ptyFile:  ptyFile,
		term:     vt10x.New(vt10x.WithSize(screenCols, screenRows)),
		output:   &lockedBuffer{},
		waitDone: make(chan struct{}),
	}

	go func() {
		session.waitErr = cmd.Wait()
		close(session.waitDone)
	}()

	go session.readOutput()

	t.Cleanup(func() {
		session.forceStop()
	})

	return session
}

func (s *tuiSession) readOutput() {
	buffer := make([]byte, 4096)
	for {
		readBytes, err := s.ptyFile.Read(buffer)
		if readBytes > 0 {
			chunk := append([]byte(nil), buffer[:readBytes]...)
			_, _ = s.output.Write(chunk)
			_, _ = s.term.Write(chunk)
			s.replyToTerminalQueries(chunk)
		}
		if err != nil {
			return
		}
	}
}

func (s *tuiSession) press(t testing.TB, keys ...string) {
	t.Helper()

	for _, key := range keys {
		_, err := s.ptyFile.WriteString(key)
		require.NoError(t, err)
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *tuiSession) snapshot() string {
	return normalizeScreen(s.term.String())
}

func (s *tuiSession) waitForAll(t testing.TB, timeout time.Duration, values ...string) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	description := strings.Join(values, ", ")
	lastScreen := s.snapshot()

	for time.Now().Before(deadline) {
		lastScreen = s.snapshot()

		allMatch := true
		for _, value := range values {
			if !strings.Contains(lastScreen, value) {
				allMatch = false
				break
			}
		}

		if allMatch {
			return lastScreen
		}
		if s.exited() {
			require.FailNowf(t, "tui exited early", "waiting for %s\nexit error: %v\nscreen:\n%s\nraw output:\n%s", description, s.waitErr, lastScreen, s.output.Tail(16_000))
		}
		time.Sleep(screenPollInterval)
	}

	require.FailNowf(t, "timed out waiting for tui screen", "waiting for %s\nscreen:\n%s\nraw output:\n%s", description, lastScreen, s.output.Tail(16_000))
	return ""
}

func (s *tuiSession) currentScreen(t testing.TB) string {
	t.Helper()

	screen := s.snapshot()
	if s.exited() {
		require.FailNowf(t, "tui exited early", "exit error: %v\nscreen:\n%s\nraw output:\n%s", s.waitErr, screen, s.output.Tail(16_000))
	}
	return screen
}

// replyToTerminalQueries manually answers OSC and DSR capability probes sent by
// bubbletea's terminal detection. Without responses the TUI stalls indefinitely.
func (s *tuiSession) replyToTerminalQueries(chunk []byte) {
	text := string(chunk)

	if !s.repliedBackground && (strings.Contains(text, "\x1b]11;?\x07") || strings.Contains(text, "\x1b]11;?\x1b\\")) {
		_, _ = s.ptyFile.WriteString("\x1b]11;rgb:0000/0000/0000\x1b\\")
		s.repliedBackground = true
	}

	if !s.repliedCursor && strings.Contains(text, "\x1b[6n") {
		_, _ = s.ptyFile.WriteString("\x1b[1;1R")
		s.repliedCursor = true
	}
}

func (s *tuiSession) exited() bool {
	select {
	case <-s.waitDone:
		return true
	default:
		return false
	}
}

func (s *tuiSession) quitAndShutdown(t testing.TB) {
	t.Helper()

	if s.exited() {
		return
	}

	s.press(t, "q")
	s.waitForAll(t, 5*time.Second, "Quit", "Keep Running", "Shut Down")
	s.press(t, "n")

	select {
	case <-s.waitDone:
		return
	case <-time.After(processExitTimeout):
		require.FailNowf(t, "tui did not exit after quit", "raw output:\n%s", s.output.Tail(16_000))
	}
}

func (s *tuiSession) forceStop() {
	s.stopOnce.Do(func() {
		if s.ptyFile != nil {
			defer s.ptyFile.Close()
		}
		if s.cmd == nil || s.cmd.Process == nil || s.exited() {
			return
		}

		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-s.waitDone:
			return
		case <-time.After(2 * time.Second):
		}
		_ = s.cmd.Process.Kill()
		select {
		case <-s.waitDone:
		case <-time.After(processExitTimeout):
		}
	})
}

func waitForRunCount(t testing.TB, client *apiclient.Client, taskName string, expectedTotal int64, timeout time.Duration) int64 {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastTotal int64

	for time.Now().Before(deadline) {
		_, total, err := client.ListRunsByTask(taskName, apiclient.RunsParams{Limit: 20})
		require.NoError(t, err)
		lastTotal = total
		if total >= expectedTotal {
			return total
		}
		time.Sleep(screenPollInterval)
	}

	require.FailNowf(t, "run count did not reach expected total", "task: %s\nexpected at least: %d\nactual: %d", taskName, expectedTotal, lastTotal)
	return lastTotal
}

func reserveTCPPort(t testing.TB) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
}

func killProcessGroup(pid int, signal syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, signal)
}

// subprocEnv builds an env slice for daemon/TUI subprocesses. It strips the
// GOCOVERDIR that `go test -covermode=atomic` injects (pointing at a temp dir
// we can't access) and re-injects our stable E2E coverage dir if
// RUNWISP_E2E_COVDIR is set. This lets `go tool covdata textfmt` merge daemon
// and TUI subprocess coverage into the final report.
//
// When RUNWISP_E2E_COVDIR is not set the binary still needs a GOCOVERDIR
// (it is always built with -cover) or it writes a warning to stderr that
// breaks tests which assert on clean stderr. Use a throwaway temp dir in
// that case.
func subprocEnv(extra ...string) []string {
	// A stray RUNWISP_SERVICE_MANAGED would make spawned daemons self-detect as
	// service-managed. Strip it so e2e behavior is host-independent. (Production
	// spawns do the same via autostart.WithoutServiceEnv.)
	base := autostart.WithoutServiceEnv(os.Environ())
	env := make([]string, 0, len(base)+len(extra)+1)
	for _, e := range base {
		if strings.HasPrefix(e, "GOCOVERDIR=") {
			continue
		}
		// The test runner (moon/CI) sets NO_COLOR on its subprocesses; left in
		// place it leaks through and makes termenv force the no-colour profile.
		// Strip it so the screenshot capture gets real SGR from its TERM/
		// COLORTERM. No-op for the default TERM=dumb sessions and non-TTY daemon
		// stdio, which never emit colour anyway.
		if strings.HasPrefix(e, "NO_COLOR=") || strings.HasPrefix(e, "CLICOLOR=") {
			continue
		}
		env = append(env, e)
	}
	covdir := os.Getenv("RUNWISP_E2E_COVDIR")
	if covdir == "" {
		covdir, _ = os.MkdirTemp("", "runwisp-nocov-")
	}
	if covdir != "" {
		env = append(env, "GOCOVERDIR="+covdir)
	}
	return append(env, extra...)
}

func normalizeScreen(screen string) string {
	lines := strings.Split(strings.ReplaceAll(screen, "\x00", ""), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.b.Write(p)
}

func (b *lockedBuffer) Tail(limit int) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	output := ansi.Strip(b.b.String())
	if len(output) <= limit {
		return output
	}
	return output[len(output)-limit:]
}

// Bytes returns a copy of the raw buffer with escape sequences intact (unlike
// Tail, which strips them). The screenshot capture replays these bytes into a
// browser terminal emulator, so the colour/cursor codes must survive.
func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]byte(nil), b.b.Bytes()...)
}

const (
	keyEnter = "\r"
	keyDown  = "\x1b[B"
	keyRight = "\x1b[C"
)
