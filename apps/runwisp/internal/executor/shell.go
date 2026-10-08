// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
)

// ShellBackend executes shell scripts on the host via the task's shell
// (default /bin/sh).
type ShellBackend struct{}

func (b *ShellBackend) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error) {
	shell, ok := def.(*model.ShellExecution)
	if !ok {
		return nil, fmt.Errorf("ShellBackend received non-shell execution: %s", def.ExecType())
	}

	if shell.Script == "" {
		return nil, fmt.Errorf("shell execution has an empty script")
	}

	// Per-execution parameters resolve to an env overlay and argv tokens. The
	// tokens are shell-quoted and appended to the script so a supplied value can
	// never break out of its quotes into the shell.
	var runParams map[string]string
	if run != nil {
		runParams = run.Params
	}
	paramEnv := model.ParamEnvLayer(task.Parameters, runParams)
	script := appendArgTokens(wrapScriptUmask(shell.Umask, shell.Script), model.ParamArgTokens(task.Parameters, runParams))

	shellPath := shell.Shell
	if shellPath == "" {
		shellPath = model.DefaultShell
	}
	cmd := exec.CommandContext(ctx, shellPath, shellArgs(shellPath, script)...)

	// Resolve run-as (user[:group]) at run time. RunUser is read from the task,
	// never from the execution def, so a station-dispatched ad-hoc run can't pick
	// a uid — privilege drop is a TOML-only capability.
	var cred *syscall.Credential
	var identity []string
	var runUserHome string
	if task.RunUser != "" {
		ra, err := resolveRunAs(task.RunUser)
		if err != nil {
			return nil, fmt.Errorf("resolve run-as for task %q: %w", task.Name, err)
		}
		cred = ra.cred
		identity = ra.identity
		runUserHome = ra.home
	}

	// env_base = "clean" replaces the daemon's environment with the minimal set
	// crond gives a job, so a task imported from a crontab runs under the
	// environment it was written against rather than whatever the daemon
	// inherited from its own launcher.
	clean := shell.EnvBase == model.EnvBaseClean
	base := os.Environ()
	if clean {
		base = cleanEnvBase(shellPath)
	}

	// Always build cmd.Env through buildProcessEnv, even with no overlays:
	// leaving it nil would make Go inherit the daemon's env verbatim, including
	// the RUNWISP_* secrets (password, station token) that buildProcessEnv strips.
	// The run-as identity (HOME/USER/LOGNAME) seeds beneath the task's own env
	// so task.Env can still override it.
	cmd.Env = buildProcessEnv(append(base, identity...), task.Env, task.Secrets, paramEnv)

	dir, err := resolveWorkingDir(shell.WorkingDir, runUserHome)
	if err != nil {
		return nil, fmt.Errorf("working_dir for task %q: %w", task.Name, err)
	}
	cmd.Dir = dir

	return startCmd(cmd, task.GracefulStopValue(), signalFromName(task.StopSignal), cred, "start command")
}

// resolveWorkingDir expands a leading `~` against the run-as user's home.
//
// config.Load absolutizes working_dir already, except a `~` on a task that
// drops to another user: that means that user's home (cron's rule), which is
// only known once the credential has been looked up. Everything else arrives
// absolute and passes through.
//
// An empty home is an error rather than a fallback to the daemon's home, which
// the dropped process may not be able to read.
func resolveWorkingDir(spec, runUserHome string) (string, error) {
	if spec != "~" && !strings.HasPrefix(spec, "~/") {
		return spec, nil
	}
	if runUserHome == "" {
		return "", fmt.Errorf("cannot expand %q: the run-as user has no home directory", spec)
	}
	return filepath.Join(runUserHome, strings.TrimPrefix(spec, "~")), nil
}

// startCmd sets up process-group isolation, graceful-stop cancellation, stdio
// pipes, and starts the command. It is the shared plumbing for ShellBackend
// and ComposeBackend — callers configure cmd.Env, cmd.Dir, and the argv
// before handing off. stopSig opens the stop ladder; the daemon always follows
// with SIGKILL after grace (and goes straight to SIGKILL when grace is
// non-positive or stopSig is already SIGKILL). cred, when non-nil, drops the
// child to another uid/gid (ShellBackend run-as); ComposeBackend passes nil.
func startCmd(cmd *exec.Cmd, grace time.Duration, stopSig syscall.Signal, cred *syscall.Credential, startErrPrefix string) (*Process, error) {
	if err := validateWorkingDir(cmd.Dir, startErrPrefix); err != nil {
		return nil, err
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: cred}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	done := make(chan struct{})
	cmd.Cancel = makeCancelFunc(cmd, grace, stopSig, done, stdout, stderr)

	if err := cmd.Start(); err != nil {
		return nil, startError(err, cred, startErrPrefix)
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
		Pid: cmd.Process.Pid,
		Rusage: func() *syscall.Rusage {
			if cmd.ProcessState == nil {
				return nil
			}
			ru, _ := cmd.ProcessState.SysUsage().(*syscall.Rusage)
			return ru
		},
	}, nil
}

// validateWorkingDir checks cmd.Dir existence here, not at config load, so a
// missing or non-directory cwd fails the run loudly with a clear message rather
// than a raw chdir error from cmd.Start.
func validateWorkingDir(dir, startErrPrefix string) error {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s: working_dir %q: %w", startErrPrefix, dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: working_dir %q is not a directory", startErrPrefix, dir)
	}
	return nil
}

// stdioCloseGrace bounds how long a run's stdout/stderr pipes may stay open
// after the stop ladder fires before they are forced shut.
//
// The executor drains the pipes to EOF before calling Process.Wait (the
// correct order for StdoutPipe/StderrPipe), so the read side has no bound of
// its own: a child that escaped the process group (setsid, a double fork)
// keeps the write end open after the tracked process dies, and the run (and a
// shutdown drain) would hang forever. cmd.WaitDelay does not cover pipes
// obtained via StdoutPipe/StderrPipe, so closeStdioAfterGrace does it by hand.
//
// A var so tests can shrink it.
var stdioCloseGrace = 10 * time.Second

// makeCancelFunc builds the cmd.Cancel callback that opens the stop ladder:
// stopSig first, then SIGKILL after grace (or straight to SIGKILL when grace is
// non-positive or stopSig is already SIGKILL). done aborts the pending kill once
// the process has been reaped. stdout/stderr are the run's pipe read ends,
// force-closed by closeStdioAfterGrace once the kill ladder has had
// stdioCloseGrace to work and the run still hasn't been reaped.
func makeCancelFunc(cmd *exec.Cmd, grace time.Duration, stopSig syscall.Signal, done <-chan struct{}, stdout, stderr io.Closer) func() error {
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		pgid := cmd.Process.Pid
		if grace <= 0 || stopSig == syscall.SIGKILL {
			err := syscall.Kill(-pgid, syscall.SIGKILL)
			go closeStdioAfterGrace(done, stdout, stderr, stdioCloseGrace)
			return err
		}
		_ = syscall.Kill(-pgid, stopSig)
		go func() {
			select {
			case <-time.After(grace):
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			case <-done:
			}
		}()
		go closeStdioAfterGrace(done, stdout, stderr, grace+stdioCloseGrace)
		return nil
	}
}

// closeStdioAfterGrace force-closes a run's stdout/stderr pipes if the run
// still hasn't been reaped (done) by the time d has elapsed since the stop
// ladder fired. Closing them unblocks any Read the executor's stream capture
// is stuck in, letting streamProcessOutput return so Execute can reach
// Process.Wait.
func closeStdioAfterGrace(done <-chan struct{}, stdout, stderr io.Closer, d time.Duration) {
	select {
	case <-done:
	case <-time.After(d):
		_ = stdout.Close()
		_ = stderr.Close()
	}
}

// startError wraps a cmd.Start failure, adding the privilege-drop hint when a
// credential was requested.
func startError(err error, cred *syscall.Credential, startErrPrefix string) error {
	if cred != nil {
		return fmt.Errorf("%s as uid=%d gid=%d: %w (the daemon must run as root to drop privileges)", startErrPrefix, cred.Uid, cred.Gid, err)
	}
	return fmt.Errorf("%s: %w", startErrPrefix, err)
}

// shellArgs builds the interpreter argv for a run script. Errexit (`-e`) makes
// a multi-line `run` block stop at the first failing command, so a failing
// middle line is never recorded as a successful run. It goes in argv rather
// than as a prepended `set -e` line so `-c` diagnostics keep the line numbers
// of the operator's script. A script opts out with `set +e`.
func shellArgs(shellPath, script string) []string {
	if model.ShellSupportsErrexit(shellPath) {
		return []string{"-e", "-c", script}
	}
	return []string{"-c", script}
}

// wrapScriptUmask prepends a `umask <octal>` line to the run script when a mask
// is configured, so the mask applies in the child only. Calling syscall.Umask
// in the daemon would be process-global and not goroutine-safe — it would race
// every other concurrent run. The umask value is digit-only (validated at
// config load), so there is no injection surface.
func wrapScriptUmask(umask, script string) string {
	if umask == "" {
		return script
	}
	return "umask " + umask + "\n" + script
}

// appendArgTokens appends shell-quoted parameter tokens to a run script,
// separated by spaces. Each token is wrapped so it reaches the program as one
// argv entry with no shell interpretation — the inertness the trust model
// requires for operator-supplied values.
//
// Trailing whitespace is trimmed first: a multiline `run` block ends in a
// newline, and appending tokens after that newline would make them a separate
// command (so a supplied value like `id` would execute the `id` binary). The
// tokens therefore attach to the script's final command, matching the
// documented "RunWisp runs `backup.sh '/data' …`" model.
func appendArgTokens(script string, tokens []string) string {
	if len(tokens) == 0 {
		return script
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(script, " \t\r\n"))
	for _, t := range tokens {
		b.WriteByte(' ')
		b.WriteString(textutil.ShellQuote(t))
	}
	return b.String()
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
