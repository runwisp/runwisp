// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
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

	// Only set cmd.Env when the task asked for overlays — leaving it nil
	// preserves Go's default of inheriting the daemon's env verbatim.
	if len(task.Env) > 0 || len(task.Secrets) > 0 {
		cmd.Env = buildProcessEnv(os.Environ(), task.Env, task.Secrets)
	}

	return startCmd(cmd, task.GracefulStop, "start command")
}

// startCmd sets up process-group isolation, graceful-stop cancellation, stdio
// pipes, and starts the command. It is the shared plumbing for ShellBackend
// and ComposeBackend — callers configure cmd.Env, cmd.Dir, and the argv
// before handing off.
func startCmd(cmd *exec.Cmd, grace time.Duration, startErrPrefix string) (*Process, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	done := make(chan struct{})
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		if grace <= 0 {
			return syscall.Kill(-pgid, syscall.SIGKILL)
		}
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
		return nil, fmt.Errorf("%s: %w", startErrPrefix, err)
	}

	var waitOnce sync.Once
	return &Process{
		Stdout: stdout,
		Stderr: stderr,
		Wait: func() (int, error) {
			err := cmd.Wait()
			waitOnce.Do(func() { close(done) })
			return exitCodeFromError(err), err
		},
		ForceKill: func() {
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		},
	}, nil
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}
