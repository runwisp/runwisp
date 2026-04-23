// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/datadir"
	"log/slog"
)

// pollHealth polls a client's health endpoint until it responds or timeout.
func pollHealth(client *apiclient.Client, timeout time.Duration) error {
	var lastErr error
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := client.HealthCheck(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return lastErr
}

// spawnDaemon starts a new daemon process in the background, detached from the
// current terminal session so it survives after the TUI exits.
func spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable: %w", err)
	}

	args := []string{"daemon",
		"--config", flags.CfgFile,
		"--data", flags.DataDir,
		"--port", strconv.Itoa(flags.Port),
	}

	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Redirect daemon stdout/stderr to a log file (truncated per spawn so
	// failure output only contains this session).
	logPath := filepath.Join(flags.DataDir, "daemon.log")
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

// shutdownDaemon sends SIGTERM to the daemon process and waits for it to exit.
func shutdownDaemon() error {
	pid, err := datadir.ReadPidFile(flags.DataDir)
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

	return waitForProcessExit(pid, 15*time.Second)
}

// waitForProcessExit polls until the daemon has exited. It checks two signals:
// 1. PID file removal — the daemon deletes its PID file on clean shutdown.
// 2. Signal 0 failure — the process no longer exists in the kernel.
func waitForProcessExit(pid int, timeout time.Duration) error {
	pidPath := datadir.PidFilePath(flags.DataDir)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid, pidPath) {
			if _, err := os.Stat(pidPath); err == nil {
				if rmErr := os.Remove(pidPath); rmErr != nil && !os.IsNotExist(rmErr) {
					slog.Warn("Failed to remove stale PID file", "path", pidPath, "err", rmErr)
				}
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("daemon (pid %d) did not shut down within %s", pid, timeout)
}

// processAlive returns true when the PID file exists AND the process
// responds to signal 0. Either side failing means the daemon is gone.
func processAlive(pid int, pidPath string) bool {
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// waitForDaemon streams the daemon log file in real-time while polling the
// health endpoint. It returns immediately on success, aborts early on fatal
// log lines or process exit, and always dumps a log tail when startup fails
// so the user can see why — regardless of which detection path tripped first.
func waitForDaemon(client *apiclient.Client, logPath string, timeout time.Duration) error {
	fmt.Fprintf(os.Stderr, "Starting daemon...\n")

	pidPath := datadir.PidFilePath(flags.DataDir)
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		logFile    *os.File
		logReader  *bufio.Reader
		pidSeen    bool
		startupErr error
		timedOut   bool
	)
	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()

	drainLog := func() (fatalMsg string, fatal bool) {
		if logReader == nil {
			if f, err := os.Open(logPath); err == nil {
				logFile = f
				logReader = bufio.NewReader(f)
			} else {
				return "", false
			}
		}
		for {
			line, err := logReader.ReadString('\n')
			if line != "" {
				line = strings.TrimRight(line, "\n\r")
				fmt.Fprintf(os.Stderr, "  %s\n", line)
				if msg, isFatal := classifyDaemonLogLine(line); isFatal {
					return msg, true
				}
			}
			if err != nil {
				// Reset so next drain picks up writes appended after EOF.
				logReader.Reset(logFile)
				return "", false
			}
		}
	}

loop:
	for {
		if msg, fatal := drainLog(); fatal {
			startupErr = fmt.Errorf("daemon failed to start: %s", msg)
			break
		}

		if err := client.HealthCheck(); err == nil {
			return nil
		}

		if _, err := os.Stat(pidPath); err == nil {
			pidSeen = true
		} else if pidSeen {
			// Give the log tailer one more drain to flush any final lines
			// from the dying daemon before we report the exit.
			time.Sleep(200 * time.Millisecond)
			drainLog()
			startupErr = errors.New("daemon process exited unexpectedly during startup")
			break
		}

		if pidSeen {
			if pid, err := datadir.ReadPidFile(flags.DataDir); err == nil {
				if !processAlive(pid, pidPath) {
					time.Sleep(200 * time.Millisecond)
					drainLog()
					startupErr = fmt.Errorf("daemon process %d exited during startup", pid)
					break
				}
			}
		}

		if time.Now().After(deadline) {
			timedOut = true
			startupErr = errors.New("daemon failed to start: health check timed out")
			break loop
		}

		<-ticker.C
	}

	// On any failure path, surface the daemon log so the user can see the
	// underlying cause. Also promote a recognised bind error to a clearer
	// message — this is the most common cause of a silent startup failure.
	logTail := tailFile(logPath, 4096)
	if hint := bindFailureHint(logTail, flags.Host, flags.Port); hint != "" {
		return errors.New(hint)
	}
	if logTail != "" {
		fmt.Fprintf(os.Stderr, "\n--- daemon log (%s) ---\n%s\n---\n\n", logPath, strings.TrimSpace(logTail))
	} else if timedOut {
		fmt.Fprintf(os.Stderr, "(daemon log at %s is empty)\n", logPath)
	}
	return startupErr
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
	}
	return "", false
}

// bindFailureHint inspects a daemon log tail for signs that the server could
// not bind its port and returns a ready-to-display error message. Returns ""
// when no bind failure is detected.
func bindFailureHint(logTail string, host string, port int) string {
	if logTail == "" {
		return ""
	}
	if !strings.Contains(logTail, "address already in use") {
		return ""
	}
	return portConflictError(host, port, errors.New("bind: address already in use")).Error()
}

// tailFile reads the last maxBytes bytes from a file.
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
