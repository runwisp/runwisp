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

	"github.com/charmbracelet/log"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/datadir"
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
		log.Warn("Failed to release daemon process", "err", err)
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
		if _, err := os.Stat(pidPath); os.IsNotExist(err) {
			return nil
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			return nil
		}
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			if rmErr := os.Remove(pidPath); rmErr != nil && !os.IsNotExist(rmErr) {
				log.Warn("Failed to remove stale PID file", "path", pidPath, "err", rmErr)
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("daemon (pid %d) did not shut down within %s", pid, timeout)
}

// waitForDaemon streams the daemon log file in real-time while polling the
// health endpoint. It returns immediately on success, aborts early on fatal
// log lines or process exit, and falls back to a static log dump on timeout.
func waitForDaemon(client *apiclient.Client, logPath string, timeout time.Duration) error {
	fmt.Fprintf(os.Stderr, "Starting daemon...\n")

	type result struct {
		err error
	}

	done := make(chan result, 1)
	stop := make(chan struct{})

	// Health poller goroutine.
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := client.HealthCheck(); err == nil {
				done <- result{}
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()

	// Log tailer goroutine.
	go func() {
		time.Sleep(50 * time.Millisecond)

		f, err := os.Open(logPath)
		if err != nil {
			return
		}
		defer f.Close()

		reader := bufio.NewReader(f)
		for {
			select {
			case <-stop:
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if line != "" {
				line = strings.TrimRight(line, "\n\r")
				fmt.Fprintf(os.Stderr, "  %s\n", line)

				if strings.Contains(line, "FATA") || strings.Contains(line, "Fatal error") {
					done <- result{
						err: fmt.Errorf("daemon failed to start: %s", line),
					}
					return
				}
			}
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				reader.Reset(f)
			}
		}
	}()

	// Process monitor goroutine.
	go func() {
		pidPath := datadir.PidFilePath(flags.DataDir)
		for i := 0; i < 20; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := os.Stat(pidPath); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		for {
			select {
			case <-stop:
				return
			default:
			}
			time.Sleep(300 * time.Millisecond)

			pid, err := datadir.ReadPidFile(flags.DataDir)
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				done <- result{err: errors.New("daemon process exited unexpectedly")}
				return
			}

			proc, err := os.FindProcess(pid)
			if err != nil {
				time.Sleep(200 * time.Millisecond)
				done <- result{err: fmt.Errorf("daemon process %d not found", pid)}
				return
			}
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				time.Sleep(200 * time.Millisecond)
				done <- result{err: fmt.Errorf("daemon process %d exited", pid)}
				return
			}
		}
	}()

	defer close(stop)

	select {
	case r := <-done:
		return r.err
	case <-time.After(timeout):
		if logTail := tailFile(logPath, 4096); logTail != "" {
			fmt.Fprintf(os.Stderr, "\n--- daemon log (%s) ---\n%s\n---\n\n", logPath, strings.TrimSpace(logTail))
		}
		return errors.New("daemon failed to start: health check timed out")
	}
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
