// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/runwisp/runwisp/internal/datadir"
)

// TestEnsureNoRunningDaemon_LiveDaemonRefused: booting a second daemon against a
// data dir whose daemon.pid names a live daemon process must fail, so it can't
// clobber the shared PID file and orphan the running daemon.
func TestEnsureNoRunningDaemon_LiveDaemonRefused(t *testing.T) {
	dir := t.TempDir()

	// A real, live process distinct from the test binary.
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })

	if err := os.WriteFile(datadir.PidFilePath(dir), []byte(strconv.Itoa(other.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := lookupProcessName
	t.Cleanup(func() { lookupProcessName = orig })
	lookupProcessName = func(int) (string, bool) { return "runwisp", true }

	if err := ensureNoRunningDaemon(Flags{DataDir: dir}); err == nil {
		t.Fatal("expected refusal to start when a live daemon owns the data dir")
	}
}

func TestEnsureNoRunningDaemon_NoPidFile(t *testing.T) {
	if err := ensureNoRunningDaemon(Flags{DataDir: t.TempDir()}); err != nil {
		t.Fatalf("no PID file should allow start, got %v", err)
	}
}

func TestEnsureNoRunningDaemon_StalePid(t *testing.T) {
	dir := t.TempDir()
	// PID 0 can't be signalled → treated as stale/not-running.
	if err := os.WriteFile(datadir.PidFilePath(dir), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureNoRunningDaemon(Flags{DataDir: dir}); err != nil {
		t.Fatalf("stale PID file should allow start, got %v", err)
	}
}

// TestProcessAlive_RejectsForeignProcess: a stale PID recycled by an unrelated
// process must not be reported alive, so stop/restart never signal a bystander.
func TestProcessAlive_RejectsForeignProcess(t *testing.T) {
	dir := t.TempDir()
	pidPath := datadir.PidFilePath(dir)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := lookupProcessName
	t.Cleanup(func() { lookupProcessName = orig })

	lookupProcessName = func(int) (string, bool) { return "sshd", true }
	if processAlive(os.Getpid(), pidPath) {
		t.Fatal("a live process not named runwisp must be treated as not-alive")
	}

	lookupProcessName = func(int) (string, bool) { return "runwisp", true }
	if !processAlive(os.Getpid(), pidPath) {
		t.Fatal("a live runwisp process must be treated as alive")
	}

	// Unknown name (e.g. macOS, no procfs) falls back to trusting liveness.
	lookupProcessName = func(int) (string, bool) { return "", false }
	if !processAlive(os.Getpid(), pidPath) {
		t.Fatal("unresolvable process name must fall back to alive")
	}
}
