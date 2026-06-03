// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// ShellBackend executes shell scripts on the host via /bin/sh.
type ShellBackend struct{}

func (b *ShellBackend) Available(_ context.Context) bool { return true }

func (b *ShellBackend) Start(ctx context.Context, task *model.Task, _ *model.Run, def model.ExecutionDef) (*Process, error) {
	shell, ok := def.(*model.ShellExecution)
	if !ok {
		return nil, fmt.Errorf("ShellBackend received non-shell execution: %s", def.ExecType())
	}

	if shell.Script == "" {
		return nil, fmt.Errorf("shell execution has an empty script")
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", shell.Script)
	// Setpgid puts the child in its own process group so SIGTERM/SIGKILL can
	// be delivered to the entire group (including grandchildren spawned by
	// the script) rather than just the /bin/sh leader.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Only set cmd.Env when the task asked for overlays — leaving it nil
	// preserves Go's default of inheriting the daemon's env verbatim.
	if len(task.Env) > 0 || len(task.Secrets) > 0 {
		cmd.Env = buildProcessEnv(os.Environ(), task.Env, task.Secrets)
	}

	grace := task.GracefulStop
	done := make(chan struct{})
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		if grace <= 0 {
			return syscall.Kill(-pgid, syscall.SIGKILL)
		}
		// Politely ask the group to exit; escalate to SIGKILL if it overstays
		// its grace period. The done channel cancels the SIGKILL when the
		// process exits cleanly during the grace window.
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		go func() {
			select {
			case <-time.After(grace):
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			case <-done:
			}
		}()
		return nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	return &Process{
		Stdout: stdout,
		Stderr: stderr,
		Wait: func() (int, error) {
			err := cmd.Wait()
			close(done)
			return exitCodeFromError(err), err
		},
		ForceKill: func() {
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		},
	}, nil
}

// exitCodeFromError extracts the process exit code from an exec error.
func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}
