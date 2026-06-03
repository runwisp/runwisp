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

// ShellBackend executes shell scripts on the host via /bin/sh. resolver
// interpolates ${...} placeholders in the script and inline env values at spawn
// time; it is nil in unit tests, which disables resolution.
type ShellBackend struct {
	resolver *SecretResolver
}

func (b *ShellBackend) Available(_ context.Context) bool { return true }

func (b *ShellBackend) Start(ctx context.Context, task *model.Task, _ *model.Run, def model.ExecutionDef) (*Process, error) {
	shell, ok := def.(*model.ShellExecution)
	if !ok {
		return nil, fmt.Errorf("ShellBackend received non-shell execution: %s", def.ExecType())
	}

	if shell.Script == "" {
		return nil, fmt.Errorf("shell execution has an empty script")
	}

	script := shell.Script
	// ${...} interpolation is a runwisp.toml feature, so it only applies to a
	// script that came from `run =` on disk (task.Run). A cloud-dispatched
	// ad-hoc script arrives as a pre-built ExecutionDef with an empty task.Run;
	// it runs verbatim. The operator who opted into cloud shell dispatch didn't
	// author that script, so resolving daemon env/files into it would both
	// change its shell semantics unexpectedly and hand daemon state to an
	// untrusted peer. TRUST MODEL: `run =` comes from disk only.
	//
	// Our ${...} forms resolve here, before /bin/sh sees the script; any bare
	// $VAR the operator wrote is still expanded by the shell at runtime. A
	// resolution failure surfaces as a start error captured in the run log.
	if task.Run != "" {
		resolved, err := b.resolver.value(shell.Script)
		if err != nil {
			return nil, fmt.Errorf("resolve script: %w", err)
		}
		script = resolved
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	// Setpgid puts the child in its own process group so SIGTERM/SIGKILL can
	// be delivered to the entire group (including grandchildren spawned by
	// the script) rather than just the /bin/sh leader.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Only set cmd.Env when the task asked for overlays — leaving it nil
	// preserves Go's default of inheriting the daemon's env verbatim.
	if len(task.Env) > 0 || len(task.SecretEnv) > 0 {
		resolvedEnv, err := b.resolver.envMap(task.Env)
		if err != nil {
			return nil, fmt.Errorf("resolve env: %w", err)
		}
		cmd.Env = buildProcessEnv(os.Environ(), resolvedEnv, task.SecretEnv)
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
