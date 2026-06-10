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

	shellPath := shell.Shell
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shellPath, "-c", wrapScriptUmask(shell.Umask, shell.Script))

	// Resolve run-as (user[:group]) at run time. RunUser is read from the task,
	// never from the execution def, so a cloud-dispatched ad-hoc run can't pick
	// a uid — privilege drop is a TOML-only capability.
	var cred *syscall.Credential
	var identity []string
	if task.RunUser != "" {
		ra, err := resolveRunAs(task.RunUser)
		if err != nil {
			return nil, fmt.Errorf("resolve run-as for task %q: %w", task.Name, err)
		}
		cred = ra.cred
		identity = ra.identity
	}

	// Only set cmd.Env when there's something to layer — leaving it nil
	// preserves Go's default of inheriting the daemon's env verbatim. The
	// run-as identity (HOME/USER/LOGNAME) seeds beneath the task's own env so
	// task.Env can still override it.
	if len(task.Env) > 0 || len(task.Secrets) > 0 || len(identity) > 0 {
		cmd.Env = buildProcessEnv(append(os.Environ(), identity...), task.Env, task.Secrets)
	}

	if shell.WorkingDir != "" {
		cmd.Dir = shell.WorkingDir
	}

	return startCmd(cmd, task.GracefulStop, signalFromName(task.StopSignal), cred, "start command")
}

// startCmd sets up process-group isolation, graceful-stop cancellation, stdio
// pipes, and starts the command. It is the shared plumbing for ShellBackend
// and ComposeBackend — callers configure cmd.Env, cmd.Dir, and the argv
// before handing off. stopSig opens the stop ladder; the daemon always follows
// with SIGKILL after grace (and goes straight to SIGKILL when grace is
// non-positive or stopSig is already SIGKILL). cred, when non-nil, drops the
// child to another uid/gid (ShellBackend run-as); ComposeBackend passes nil.
func startCmd(cmd *exec.Cmd, grace time.Duration, stopSig syscall.Signal, cred *syscall.Credential, startErrPrefix string) (*Process, error) {
	// working_dir existence is checked here, not at config load, so a missing
	// or non-directory cwd fails the run loudly with a clear message rather
	// than a raw chdir error from cmd.Start.
	if cmd.Dir != "" {
		info, err := os.Stat(cmd.Dir)
		if err != nil {
			return nil, fmt.Errorf("%s: working_dir %q: %w", startErrPrefix, cmd.Dir, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s: working_dir %q is not a directory", startErrPrefix, cmd.Dir)
		}
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: cred}

	done := make(chan struct{})
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		if grace <= 0 || stopSig == syscall.SIGKILL {
			return syscall.Kill(-pgid, syscall.SIGKILL)
		}
		_ = syscall.Kill(-pgid, stopSig)
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
		if cred != nil {
			return nil, fmt.Errorf("%s as uid=%d gid=%d: %w (the daemon must run as root to drop privileges)", startErrPrefix, cred.Uid, cred.Gid, err)
		}
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

// wrapScriptUmask prepends a `umask <octal>` line to the run script when a mask
// is configured, so the mask applies in the child only. Calling syscall.Umask
// in the daemon would be process-global and not goroutine-safe — it would race
// every other concurrent run. The umask value is digit-only (validated at
// config load), so there is no injection surface. We deliberately do not `exec`
// the script: RunWisp already signals the whole process group (-pgid), so it
// doesn't matter that the shell stays the group leader, and `exec` cannot wrap
// an arbitrary compound run script.
func wrapScriptUmask(umask, script string) string {
	if umask == "" {
		return script
	}
	return "umask " + umask + "\n" + script
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
