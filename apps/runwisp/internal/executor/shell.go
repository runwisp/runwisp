// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// defaultShell mirrors config.DefaultShell. It is duplicated rather than
// imported because the executor must not depend on the config package; the two
// are pinned together by TestShellArgs_DefaultShellMatchesConfig.
const defaultShell = "/bin/sh"

// ShellBackend executes shell scripts on the host via /bin/sh.
type ShellBackend struct{}

func (b *ShellBackend) Available(_ context.Context) bool { return true }

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
		shellPath = defaultShell
	}
	cmd := exec.CommandContext(ctx, shellPath, shellArgs(shellPath, script)...)

	// Resolve run-as (user[:group]) at run time. RunUser is read from the task,
	// never from the execution def, so a cloud-dispatched ad-hoc run can't pick
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

	// Only set cmd.Env when there's something to layer — leaving it nil
	// preserves Go's default of inheriting the daemon's env verbatim. A clean
	// base counts as something to layer even with no task env at all: that case
	// is precisely the one where dropping the daemon's variables is the whole
	// instruction. The run-as identity (HOME/USER/LOGNAME) seeds beneath the
	// task's own env so task.Env can still override it.
	if clean || len(task.Env) > 0 || len(task.Secrets) > 0 || len(identity) > 0 || len(paramEnv) > 0 {
		cmd.Env = buildProcessEnv(append(base, identity...), task.Env, task.Secrets, paramEnv)
	}

	dir, err := resolveWorkingDir(shell.WorkingDir, runUserHome)
	if err != nil {
		return nil, fmt.Errorf("working_dir for task %q: %w", task.Name, err)
	}
	cmd.Dir = dir

	return startCmd(cmd, task.GracefulStop, signalFromName(task.StopSignal), cred, "start command")
}

// resolveWorkingDir expands a leading `~` against the run-as user's home.
//
// config.Load absolutizes working_dir already, and leaves exactly one case for
// here: a `~` on a task that also drops to another user. That means *that* user's
// home — cron's rule, which is what makes a system crontab reproducible — and it
// can only be answered once the credential has been looked up, next to the
// lookup that produced it. Everything else arrives absolute and passes through.
//
// An empty home is an error rather than a fallback: the daemon's own home would
// be a directory the dropped process may not even be able to read, and silently
// running somewhere other than where the task said is how a job writes its
// output into the void.
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

	done := make(chan struct{})
	cmd.Cancel = makeCancelFunc(cmd, grace, stopSig, done)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

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

// makeCancelFunc builds the cmd.Cancel callback that opens the stop ladder:
// stopSig first, then SIGKILL after grace (or straight to SIGKILL when grace is
// non-positive or stopSig is already SIGKILL). done aborts the pending kill once
// the process has been reaped.
func makeCancelFunc(cmd *exec.Cmd, grace time.Duration, stopSig syscall.Signal, done <-chan struct{}) func() error {
	return func() error {
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
}

// startError wraps a cmd.Start failure, adding the privilege-drop hint when a
// credential was requested.
func startError(err error, cred *syscall.Credential, startErrPrefix string) error {
	if cred != nil {
		return fmt.Errorf("%s as uid=%d gid=%d: %w (the daemon must run as root to drop privileges)", startErrPrefix, cred.Uid, cred.Gid, err)
	}
	return fmt.Errorf("%s: %w", startErrPrefix, err)
}

// shellArgs builds the interpreter argv for a run script. Fail-fast (`-e`,
// errexit) is armed here so a multi-line `run` block stops at the first failing
// command instead of running on and reporting the *last* command's exit code —
// without it, a script whose middle line fails is persisted as a successful
// run, which is the invisible failure the daemon exists to prevent.
//
// Why `-e` in argv rather than a `set -e` line prepended to the script: both
// dash and bash number `-c` diagnostics relative to the script string, so
// prepending a line shifts every error message in every failing run by one.
// The operator reads "line 7", counts to line 7 of their `run` block, and finds
// the wrong command. Passing the flag costs no offset and leaves the executed
// script byte-identical to what the TOML says.
//
// Only `-e`. `-u` (unset variables) is unrelated strictness and breaks scripts
// that legitimately read optional env vars. `-o pipefail` is not POSIX — dash
// rejects it outright — so a task that needs it selects `shell = "/bin/bash"`
// and writes `set -o pipefail` itself.
//
// Opting out needs no config key: `set +e` as the script's first line turns
// errexit back off, because a runtime `set` overrides the argv flag.
//
// One fidelity cost, accepted knowingly: bash-as-/bin/sh (macOS) reports exit 1
// instead of 127 when errexit aborts on a missing *absolute path*, though a
// failed PATH lookup still reports 127 and dash reports 127 for both. A less
// specific exit code on one dev platform is a small price for not recording
// failed runs as successes on every platform.
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
		b.WriteString(shellQuote(t))
	}
	return b.String()
}

// shellQuote single-quote-wraps a token for /bin/sh, rendering an embedded
// single quote as the classic '\” sequence. Single quotes suppress every
// shell metacharacter, so a value like `'; rm -rf /` becomes an inert literal.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
