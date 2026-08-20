// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/datadir"
)

// pollUntil calls check every 200ms until it returns true or deadline passes,
// reporting which happened.
func pollUntil(deadline time.Time, check func() bool) bool {
	for time.Now().Before(deadline) {
		if check() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func pollHealth(client *apiclient.Client, timeout time.Duration) error {
	var lastErr error
	ok := pollUntil(time.Now().Add(timeout), func() bool {
		lastErr = client.HealthCheck()
		return lastErr == nil
	})
	if ok {
		return nil
	}
	return lastErr
}

// spawnDaemon starts a new daemon process in the background, detached from the
// current terminal session so it survives after the TUI exits.
func spawnDaemon(f Flags) error {
	return spawnDaemonProcess(daemonSpawnArgs([]string{"daemon"}, f), f.DataDir)
}

// daemonSpawnArgs builds the argument list for a spawned daemon, prefixed by
// the given subcommand ("daemon", or "cloud --no-tui"). It carries the full
// effective config — including --host and --socket — so the child binds exactly
// where the launcher probed instead of silently re-defaulting to loopback / the
// default socket path.
func daemonSpawnArgs(subcommand []string, f Flags) []string {
	args := append([]string{}, subcommand...)
	args = append(args,
		"--config", f.CfgFile,
		"--data", f.DataDir,
		"--port", strconv.Itoa(f.Port),
		"--host", f.Host,
	)
	if f.Socket != "" {
		args = append(args, "--socket", f.Socket)
	}
	return args
}

// spawnDaemonProcess execs `runwisp <args...>` as a detached background process
// (new session, stdio redirected to the data dir's daemon.log) so it outlives
// the foreground process that launched it. The leading arg selects the
// subcommand — "daemon" for standalone, "cloud --no-tui" for cloud mode.
func spawnDaemonProcess(args []string, dataDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable: %w", err)
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// runwisp is launching this daemon itself, so it is not init-managed.
	// Strip any leaked service-manager markers (e.g. INVOCATION_ID inherited
	// from a systemd-scoped terminal) so the daemon doesn't self-report as
	// service-managed and hide the TUI's "Shut Down" quit option.
	cmd.Env = autostart.WithoutServiceEnv(os.Environ())

	// Redirect daemon stdout/stderr to a log file (truncated per spawn so
	// failure output only contains this session).
	logPath := filepath.Join(dataDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("cannot open daemon log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("cannot start daemon process: %w", err)
	}

	if err := cmd.Process.Release(); err != nil {
		slog.Warn("Failed to release daemon process", "err", err)
	}
	logFile.Close()
	return nil
}

// shutdownDaemon sends SIGTERM to the daemon process and waits for it to
// exit, with the default grace window. Used as the TUI's shutdown callback.
func shutdownDaemon(f Flags) error {
	return shutdownDaemonWait(15*time.Second, f)
}

// shutdownDaemonWait is shutdownDaemon with a caller-chosen wait window —
// `runwisp stop`/`restart` size it from [daemon] shutdown_timeout so a
// long-draining daemon isn't reported as stuck.
func shutdownDaemonWait(timeout time.Duration, f Flags) error {
	pid, err := datadir.ReadPidFile(f.DataDir)
	if err != nil {
		return fmt.Errorf("cannot read daemon PID file: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("cannot find daemon process %d: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("cannot signal daemon process %d: %w", pid, err)
	}

	fmt.Fprintf(os.Stderr, "Waiting for daemon (pid %d) to shut down...\n", pid)

	return waitForProcessExit(pid, timeout, f.DataDir)
}

// waitForProcessExit polls until the daemon has exited. It checks two signals:
// 1. PID file removal — the daemon deletes its PID file on clean shutdown.
// 2. Signal 0 failure — the process no longer exists in the kernel.
func waitForProcessExit(pid int, timeout time.Duration, dataDir string) error {
	pidPath := datadir.PidFilePath(dataDir)
	if !pollUntil(time.Now().Add(timeout), func() bool { return !processAlive(pid, pidPath) }) {
		return fmt.Errorf("daemon (pid %d) did not shut down within %s", pid, timeout)
	}
	if _, err := os.Stat(pidPath); err == nil {
		if rmErr := os.Remove(pidPath); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("Failed to remove stale PID file", "path", pidPath, "err", rmErr)
		}
	}
	return nil
}

// processAlive returns true when the PID file exists, the process responds to
// signal 0, AND the process still looks like a RunWisp daemon. The identity
// check closes a PID-reuse hole: after an unclean death leaves a stale
// daemon.pid, the OS may recycle that PID for an unrelated process, and
// signalling it (stop/restart) would hit an innocent bystander.
func processAlive(pid int, pidPath string) bool {
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	return processIsDaemon(pid)
}

// lookupProcessName resolves a PID to its process name. Swapped out in tests.
var lookupProcessName = defaultLookupProcessName

// defaultLookupProcessName reads the process name from /proc/<pid>/comm on
// Linux. On platforms without procfs (macOS) the read fails and ok is false,
// so processIsDaemon falls back to trusting the liveness check alone.
func defaultLookupProcessName(pid int) (name string, ok bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// processIsDaemon reports whether the process with the given PID looks like a
// RunWisp daemon. It is best-effort: when the process name can't be read it
// returns true so a genuine daemon is never mistaken for a recycled stranger.
func processIsDaemon(pid int) bool {
	name, ok := lookupProcessName(pid)
	if !ok {
		return true
	}
	return strings.Contains(strings.ToLower(name), "runwisp")
}

// daemonLogDrainer tails the daemon's log file incrementally, emitting each
// new line to stderr and detecting fatal startup messages.
type daemonLogDrainer struct {
	path   string
	file   *os.File
	reader *bufio.Reader
}

// drain reads every newly-appended line, echoing non-fatal ones to stderr live
// so a slow-but-healthy startup shows real progress. Once a fatal line is
// found, echoing stops and every remaining line already on disk is folded
// into fatalMsg instead — the CLI error renderer writes its title followed by
// blank-line-separated bullet details in one burst, and by the time this
// polls, a daemon that died that fast has already flushed all of it. The
// caller re-renders fatalMsg as the one polished error box; echoing the same
// lines raw first would just print the failure twice.
func (d *daemonLogDrainer) drain() (fatalMsg string, fatal bool) {
	if d.reader == nil {
		f, err := os.Open(d.path)
		if err != nil {
			return "", false
		}
		d.file = f
		d.reader = bufio.NewReader(f)
	}
	var fatalLines []string
	for {
		line, err := d.reader.ReadString('\n')
		if line != "" {
			fatalLines = d.handleLine(strings.TrimRight(line, "\n\r"), fatalLines)
		}
		if err != nil {
			// Reset so next drain picks up writes appended after EOF.
			d.reader.Reset(d.file)
			if fatalLines != nil {
				return strings.Join(fatalLines, "\n"), true
			}
			return "", false
		}
	}
}

// handleLine echoes a non-fatal line live, or — once a fatal line has been
// seen — folds it into fatalLines instead (see drain's doc comment for why).
func (d *daemonLogDrainer) handleLine(line string, fatalLines []string) []string {
	if fatalLines != nil {
		return append(fatalLines, line)
	}
	if msg, isFatal := classifyDaemonLogLine(line); isFatal {
		return []string{msg}
	}
	fmt.Fprintf(os.Stderr, "  %s\n", line)
	return fatalLines
}

func (d *daemonLogDrainer) close() {
	if d.file != nil {
		d.file.Close()
	}
}

// waitForDaemon streams the daemon log file in real-time while polling the
// health endpoint. It returns immediately on success, aborts early on fatal
// log lines or process exit, and always dumps a log tail when startup fails
// so the user can see why — regardless of which detection path tripped first.
func waitForDaemon(client *apiclient.Client, logPath string, timeout time.Duration, f Flags) error {
	fmt.Fprintf(os.Stderr, "Starting daemon...\n")

	drainer := &daemonLogDrainer{path: logPath}
	defer drainer.close()

	pidPath := datadir.PidFilePath(f.DataDir)
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	startupErr, timedOut := waitForDaemonLoop(client, drainer, pidPath, deadline, ticker, f.DataDir)
	if startupErr == nil {
		return nil
	}

	// The drainer has already streamed every log line live, so we don't reprint
	// them. We still read the tail to promote a recognised bind error to a
	// clearer message (the most common silent-startup cause), and to explain a
	// timeout when the daemon logged nothing at all.
	logTail := tailFile(logPath, 4096)
	if hint := bindFailureHint(logTail, f.Host, f.Port); hint != nil {
		return hint
	}
	if timedOut && logTail == "" {
		fmt.Fprintf(os.Stderr, "(daemon log at %s is empty)\n", logPath)
	}
	return startupErr
}

// waitForDaemonLoop is the polling core of waitForDaemon. It returns the
// startup error (nil on success) and whether the loop exited due to timeout.
func waitForDaemonLoop(client *apiclient.Client, drainer *daemonLogDrainer, pidPath string, deadline time.Time, ticker *time.Ticker, dataDir string) (startupErr error, timedOut bool) {
	var pidSeen bool
	for {
		if msg, fatal := drainer.drain(); fatal {
			return fmt.Errorf("daemon failed to start: %s", msg), false
		}

		if err := client.HealthCheck(); err == nil {
			return nil, false
		}

		if _, err := os.Stat(pidPath); err == nil {
			pidSeen = true
		} else if pidSeen {
			// Give the log tailer one more drain to flush any final lines
			// from the dying daemon before we report the exit.
			time.Sleep(200 * time.Millisecond)
			drainer.drain()
			return errors.New("daemon process exited unexpectedly during startup"), false
		}

		if pidSeen {
			if pid, alive := checkPidAlive(pidPath, dataDir); !alive {
				time.Sleep(200 * time.Millisecond)
				drainer.drain()
				return fmt.Errorf("daemon process %d exited during startup", pid), false
			}
		}

		if time.Now().After(deadline) {
			return errors.New("daemon failed to start: health check timed out"), true
		}

		<-ticker.C
	}
}

// checkPidAlive reads the PID file and returns (pid, alive). Returns (0, true)
// when the file is unreadable (conservative: assume alive to avoid false exits).
func checkPidAlive(pidPath, dataDir string) (pid int, alive bool) {
	p, err := datadir.ReadPidFile(dataDir)
	if err != nil {
		return 0, true
	}
	return p, processAlive(p, pidPath)
}

// classifyDaemonLogLine returns a trimmed message and fatal=true when the
// given daemon log line indicates an unrecoverable startup failure.
func classifyDaemonLogLine(line string) (string, bool) {
	switch {
	case strings.Contains(line, "FATA"),
		strings.Contains(line, "Fatal error"),
		strings.Contains(line, "Server failed"),
		strings.Contains(line, "address already in use"),
		strings.Contains(line, "bind: permission denied"):
		return line, true
	// handleCLIError/renderError print an unbracketed "ERROR" badge right
	// before the process exits on any fatal top-level command error (missing
	// config, bad TLS cert, ...) — unlike slog's routine "[ERROR]" lines, which
	// don't mean the process is dying. Catching it generically here lets
	// waitForDaemonLoop report the real cause immediately instead of waiting
	// out the full health-check timeout. The badge itself is stripped: the
	// caller wraps this message in its own "daemon failed to start" error,
	// which then goes through the same renderer and would otherwise print two
	// "ERROR" badges back to back.
	case strings.Contains(line, "ERROR") && !strings.Contains(line, "[ERROR]"):
		if _, title, ok := strings.Cut(line, "ERROR"); ok {
			return strings.TrimSpace(title), true
		}
		return line, true
	}
	return "", false
}

// bindFailureHint inspects a daemon log tail for signs that the server could
// not bind its port and returns a ready-to-display user-facing error. Returns
// nil when no bind failure is detected.
func bindFailureHint(logTail, host string, port int) error {
	if logTail == "" {
		return nil
	}
	if !strings.Contains(logTail, "address already in use") {
		return nil
	}
	return portConflictError(host, port, errors.New("bind: address already in use"))
}

func tailFile(path string, maxBytes int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return ""
	}

	size := stat.Size()
	if size == 0 {
		return ""
	}

	offset := int64(0)
	if size > maxBytes {
		offset = size - maxBytes
	}

	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return ""
	}
	return string(buf)
}
