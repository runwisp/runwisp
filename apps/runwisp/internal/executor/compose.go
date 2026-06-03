// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// composeAvailableTimeout caps the `docker compose version` probe we use to
// decide whether the backend is wired up at all. Short on purpose — when the
// CLI is installed it answers in milliseconds, and when it isn't there's no
// payoff to a longer wait.
const composeAvailableTimeout = 2 * time.Second

// ComposeBackend executes docker-compose-declared services by shelling out to
// the `docker compose` CLI. The CLI gates this entirely: composespec is used
// at config-load to enumerate services (offline), but every actual container
// spawn goes through `docker compose run --rm` (or `up` in stack mode).
type ComposeBackend struct {
	// dockerCmd is the binary name; "docker" by default. Tests inject a shim.
	dockerCmd string
	// resolver interpolates ${...} in inline env values before they are passed
	// to the container as -e flags. Nil in unit tests disables resolution.
	resolver *SecretResolver
}

// NewComposeBackend returns a ComposeBackend ready for use. Availability is
// not probed eagerly — wrap in NewLazyComposeBackend when you want startup
// to survive a missing or slow docker CLI.
func NewComposeBackend(resolver *SecretResolver) *ComposeBackend {
	return &ComposeBackend{dockerCmd: "docker", resolver: resolver}
}

// Available probes `docker compose version` with a short timeout. Returns
// false when the binary is missing, the daemon is unreachable, or the call
// exceeds composeAvailableTimeout.
func (b *ComposeBackend) Available(ctx context.Context) bool {
	if b == nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, composeAvailableTimeout)
	defer cancel()
	return exec.CommandContext(probeCtx, b.dockerCmd, "compose", "version").Run() == nil
}

func (b *ComposeBackend) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error) {
	ce, ok := def.(*model.ComposeExecution)
	if !ok {
		return nil, fmt.Errorf("ComposeBackend received non-compose execution: %s", def.ExecType())
	}
	if ce.File == "" {
		return nil, fmt.Errorf("compose execution missing file path")
	}

	// Resolve ${...} in inline env values before they become -e flags. We copy
	// the task rather than mutating the shared one so the resolved secret lives
	// only in this spawn's argv, never back in the in-memory task set.
	task, err := b.resolveTaskEnv(task)
	if err != nil {
		return nil, err
	}

	args := buildComposeArgs(ce, task, run)
	cmd := exec.CommandContext(ctx, b.dockerCmd, args...)
	if ce.WorkingDir != "" {
		cmd.Dir = ce.WorkingDir
	}
	// Mirror ShellBackend: own process group so SIGTERM/SIGKILL reaches the
	// whole subtree (docker CLI + grandchildren it may spawn).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Compose CLI inherits the daemon's env so users' DOCKER_HOST etc. work;
	// task env is passed to the container itself via -e flags (see
	// buildComposeArgs). Note: we deliberately do NOT overlay task.Env onto
	// the compose CLI process, only into the target container.
	cmd.Env = os.Environ()

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
		return nil, fmt.Errorf("start docker compose: %w", err)
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

// resolveTaskEnv returns a shallow task copy whose Env values have every
// ${...} placeholder resolved. It returns the task unchanged when there is no
// resolver or no inline env, preserving the no-overlay fast path. SecretEnv is
// left as-is — it carries already-final values, not placeholders.
func (b *ComposeBackend) resolveTaskEnv(task *model.Task) (*model.Task, error) {
	if b.resolver == nil || len(task.Env) == 0 {
		return task, nil
	}
	resolved, err := b.resolver.envMap(task.Env)
	if err != nil {
		return nil, fmt.Errorf("resolve compose env: %w", err)
	}
	cp := *task
	cp.Env = resolved
	return &cp, nil
}

// buildComposeArgs assembles the argv tail (after the docker binary) for
// either per-service (`run --rm`) or stack-mode (`up --abort-on-container-exit`)
// invocations. RUNWISP_INSTANCE_INDEX + task.Env + task.SecretEnv flow into
// the target container via repeated -e flags, deterministically ordered.
func buildComposeArgs(ce *model.ComposeExecution, task *model.Task, run *model.Run) []string {
	args := []string{"compose", "-f", ce.File}
	if ce.ProjectName != "" {
		args = append(args, "-p", ce.ProjectName)
	}
	for _, p := range ce.Profiles {
		args = append(args, "--profile", p)
	}
	for _, ef := range ce.EnvFile {
		args = append(args, "--env-file", ef)
	}

	switch ce.Mode {
	case model.ComposeModeStack:
		args = append(args, "up", "--abort-on-container-exit", "--no-log-prefix")
	default:
		args = append(args, "run", "--rm", "--service-ports", "--use-aliases")
		if !ce.WithDeps {
			args = append(args, "--no-deps")
		}
		if ce.Pull != "" && ce.Pull != model.ComposePullMissing {
			args = append(args, "--pull", ce.Pull)
		}
		instanceIndex := 0
		if run != nil {
			instanceIndex = run.InstanceIndex
		}
		args = append(args, "--name", composeContainerName(ce.ProjectName, ce.Service, instanceIndex))
		for _, kv := range composeEnvFlags(task, instanceIndex) {
			args = append(args, "-e", kv)
		}
		args = append(args, ce.Service)
	}
	return args
}

// composeContainerName mirrors docker compose's own naming (`<project>_<svc>_<index>`)
// so `docker compose ps` shows each RunWisp instance as a separately named
// container. Falls back to service-only names if the project/service is empty
// (defensive — both should always be set by the time we get here).
func composeContainerName(project, service string, idx int) string {
	switch {
	case project == "" && service == "":
		return ""
	case project == "":
		return fmt.Sprintf("%s_%d", service, idx)
	case service == "":
		return fmt.Sprintf("%s_%d", project, idx)
	default:
		return fmt.Sprintf("%s_%s_%d", project, service, idx)
	}
}

// composeEnvFlags returns deterministically ordered KEY=VALUE strings suitable
// for passing as -e arguments to `docker compose run`. RUNWISP_INSTANCE_INDEX
// is always injected; task.Env wins over the daemon's environment because we
// only forward the user's declared variables, not os.Environ().
func composeEnvFlags(task *model.Task, instanceIndex int) []string {
	merged := map[string]string{
		"RUNWISP_INSTANCE_INDEX": strconv.Itoa(instanceIndex),
	}
	for k, v := range task.Env {
		merged[k] = v
	}
	for k, v := range task.SecretEnv {
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + merged[k]
	}
	return out
}

// LazyComposeBackend defers the docker compose availability probe until first
// use. Mirrors LazyContainerBackend so the daemon boots fast even when the
// docker CLI is slow to respond (or absent).
type LazyComposeBackend struct {
	mu       sync.Mutex
	backend  *ComposeBackend
	probed   bool
	avail    bool
	resolver *SecretResolver
}

// NewLazyComposeBackend returns a backend that probes `docker compose` on
// first call to Available()/Start(). The resolver is handed to the real
// backend once probed.
func NewLazyComposeBackend(resolver *SecretResolver) *LazyComposeBackend {
	return &LazyComposeBackend{resolver: resolver}
}

func (l *LazyComposeBackend) ensureProbed(ctx context.Context) (*ComposeBackend, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.probed {
		return l.backend, l.avail
	}
	l.backend = NewComposeBackend(l.resolver)
	l.avail = l.backend.Available(ctx)
	l.probed = true
	return l.backend, l.avail
}

func (l *LazyComposeBackend) Available(ctx context.Context) bool {
	_, ok := l.ensureProbed(ctx)
	return ok
}

func (l *LazyComposeBackend) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error) {
	b, ok := l.ensureProbed(ctx)
	if !ok {
		return nil, fmt.Errorf("docker compose unavailable: install Docker (with the compose plugin) or check that `docker compose version` succeeds")
	}
	return b.Start(ctx, task, run, def)
}
