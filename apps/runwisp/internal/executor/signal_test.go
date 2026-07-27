// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package executor

import (
	"bufio"
	"context"
	"io"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalFromName(t *testing.T) {
	cases := map[string]syscall.Signal{
		"SIGTERM": syscall.SIGTERM,
		"term":    syscall.SIGTERM,
		"INT":     syscall.SIGINT,
		"SIGINT":  syscall.SIGINT,
		"SIGQUIT": syscall.SIGQUIT,
		"SIGHUP":  syscall.SIGHUP,
		"SIGKILL": syscall.SIGKILL,
		"SIGUSR1": syscall.SIGUSR1,
		"SIGUSR2": syscall.SIGUSR2,
		"":        syscall.SIGTERM, // empty falls back to the default
		"bogus":   syscall.SIGTERM, // unknown falls back (validation rejects earlier)
	}
	for in, want := range cases {
		assert.Equalf(t, want, signalFromName(in), "signalFromName(%q)", in)
	}
}

// TestShellBackend_StopSignalDeliversConfiguredSignal is the bug-first proof
// that stop_signal is wired through, not just stored: the script ignores
// SIGTERM but exits 42 on SIGINT. With stop_signal="SIGINT" the run exits via
// its trap almost immediately; before the wiring it would sit through the full
// graceful_stop window and die by SIGKILL (exit code -1).
func TestShellBackend_StopSignalDeliversConfiguredSignal(t *testing.T) {
	script := `trap '' TERM
trap 'exit 42' INT
echo ready
while true; do sleep 0.05; done
`
	task := &model.Task{Name: "sig", GracefulStop: 5 * time.Second, StopSignal: "SIGINT"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &ShellBackend{}
	proc, err := backend.Start(ctx, task, nil, &model.ShellExecution{Script: script})
	require.NoError(t, err)

	// Barrier: wait until the traps are installed and the script signals ready.
	br := bufio.NewReader(proc.Stdout)
	line, _ := br.ReadString('\n')
	require.Contains(t, line, "ready")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(io.Discard, br) }()
	go func() { defer wg.Done(); _, _ = io.Copy(io.Discard, proc.Stderr) }()

	cancel()
	exitCode, _ := proc.Wait()
	wg.Wait()

	assert.Equal(t, 42, exitCode, "run should exit via its SIGINT trap, not be SIGKILLed after grace")
}
