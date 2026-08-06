// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package executor

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeBackend_BuildArgs_ServicesMode(t *testing.T) {
	ce := &model.ComposeExecution{
		File:        "./docker-compose.yml",
		ProjectName: "myapp",
		Service:     "web",
		Mode:        model.ComposeModeServices,
		Profiles:    []string{"prod"},
		EnvFile:     []string{"./.env"},
		Pull:        model.ComposePullAlways,
	}
	task := &model.Task{
		Name: "boxes.web",
		Env:  map[string]string{"LOG_LEVEL": "info"},
	}
	run := &model.Run{InstanceIndex: 2}

	args := buildComposeArgs(ce, task, run, "fp-123")
	joined := strings.Join(args, " ")

	assert.Contains(t, joined, "compose -f ./docker-compose.yml")
	assert.Contains(t, joined, "-p myapp")
	assert.Contains(t, joined, "--profile prod")
	assert.Contains(t, joined, "--env-file ./.env")
	assert.Contains(t, joined, "run --rm --service-ports --use-aliases")
	assert.Contains(t, joined, "--no-deps")
	assert.Contains(t, joined, "--pull always")
	assert.Contains(t, joined, "--name myapp_web_2")
	assert.Contains(t, joined, "--label com.runwisp.managed=true")
	assert.Contains(t, joined, "--label com.runwisp.task=boxes.web")
	assert.Contains(t, joined, "--label com.runwisp.instance=2")
	assert.Contains(t, joined, "--label com.runwisp.instance-fp=fp-123")
	// Values are injected into the docker CLI's own environment, not argv, so
	// only the value-less `-e KEY` flags appear here.
	assert.Contains(t, joined, "-e RUNWISP_INSTANCE_INDEX")
	assert.Contains(t, joined, "-e LOG_LEVEL")
	assert.NotContains(t, joined, "LOG_LEVEL=info", "env values must never appear on argv")
	// service name is the final positional argument
	assert.Equal(t, "web", args[len(args)-1])
}

func TestComposeBackend_BuildArgs_ExecMode(t *testing.T) {
	ce := &model.ComposeExecution{
		File:        "./compose.yaml",
		ProjectName: "myapp",
		Service:     "app",
		Mode:        model.ComposeModeExec,
		Command:     "php artisan schedule:run",
		Profiles:    []string{"prod"},
		EnvFile:     []string{"./.env"},
		// Create-time settings are meaningless for exec and must not leak into
		// the argv even when a [compose.*] block set them.
		Pull:     model.ComposePullAlways,
		WithDeps: true,
	}
	task := &model.Task{Name: "myapp.schedule", Env: map[string]string{"LOG_LEVEL": "info"}}

	args := buildComposeArgs(ce, task, &model.Run{InstanceIndex: 0}, "fp-123")
	joined := strings.Join(args, " ")

	assert.Contains(t, joined, "compose -f ./compose.yaml")
	assert.Contains(t, joined, "-p myapp")
	assert.Contains(t, joined, "--profile prod")
	assert.Contains(t, joined, "--env-file ./.env")
	assert.Contains(t, joined, "exec -T")
	// Values are injected into the docker CLI's own environment, not argv, so
	// only the value-less `-e KEY` flag appears here.
	assert.Contains(t, joined, "-e LOG_LEVEL")
	assert.NotContains(t, joined, "LOG_LEVEL=info", "env values must never appear on argv")

	// The script reaches the container's shell with fail-fast armed, byte-identical.
	require.GreaterOrEqual(t, len(args), 5)
	tail := args[len(args)-5:]
	assert.Equal(t, []string{"app", "/bin/sh", "-e", "-c", "php artisan schedule:run"}, tail)

	// None of the create-a-container flags belong here: the container already
	// exists and is not ours.
	for _, forbidden := range []string{"--rm", "--name", "--label", "--pull", "--no-deps", "--service-ports", "--use-aliases", "run"} {
		assert.NotContains(t, args, forbidden, "exec mode must not pass %q", forbidden)
	}
}

// -T is passed explicitly rather than relying on compose's TTY detection: an
// allocated TTY folds stderr into stdout and appends \r to every captured line.
func TestComposeBackend_BuildArgs_ExecModeAlwaysDisablesTTY(t *testing.T) {
	ce := &model.ComposeExecution{
		File:    "./compose.yaml",
		Service: "app",
		Mode:    model.ComposeModeExec,
		Command: "true",
	}
	args := buildComposeArgs(ce, &model.Task{}, nil, "")

	execIdx := -1
	for i, a := range args {
		if a == "exec" {
			execIdx = i
			break
		}
	}
	require.NotEqual(t, -1, execIdx, "expected an `exec` token in %v", args)
	assert.Equal(t, "-T", args[execIdx+1], "-T must immediately follow exec")
}

func TestComposeBackend_BuildArgs_ExecModeAppendsParamTokens(t *testing.T) {
	ce := &model.ComposeExecution{
		File:    "./compose.yaml",
		Service: "app",
		Mode:    model.ComposeModeExec,
		Command: "./backup.sh",
	}
	task := &model.Task{
		Name: "backup",
		Parameters: []model.TaskParam{
			{Kind: model.ParamArg, Key: "target"},
		},
	}
	run := &model.Run{Params: map[string]string{"target": "nightly; rm -rf /"}}

	args := buildComposeArgs(ce, task, run, "")
	// Shell-quoted into the script text, exactly as the host shell backend does,
	// so a hostile parameter value is an inert literal rather than a second
	// command running inside the user's container.
	assert.Equal(t, `./backup.sh 'nightly; rm -rf /'`, args[len(args)-1])
}

func TestComposeBackend_BuildArgs_StackMode(t *testing.T) {
	ce := &model.ComposeExecution{
		File:        "./docker-compose.yml",
		ProjectName: "myapp",
		Mode:        model.ComposeModeStack,
	}
	args := buildComposeArgs(ce, &model.Task{}, nil, "")
	joined := strings.Join(args, " ")

	assert.Contains(t, joined, "compose -f ./docker-compose.yml -p myapp up --abort-on-container-exit --no-log-prefix")
	assert.NotContains(t, joined, "--name", "stack mode lets compose name the containers")
	assert.NotContains(t, joined, "-e ", "stack mode forwards env via compose, not -e flags")
}

func TestComposeBackend_BuildArgs_OmitsNoDepsWhenWithDeps(t *testing.T) {
	ce := &model.ComposeExecution{
		File:     "./docker-compose.yml",
		Service:  "web",
		Mode:     model.ComposeModeServices,
		WithDeps: true,
	}
	args := buildComposeArgs(ce, &model.Task{}, nil, "")
	assert.NotContains(t, strings.Join(args, " "), "--no-deps")
}

func TestComposeBackend_BuildArgs_OmitsPullWhenMissing(t *testing.T) {
	ce := &model.ComposeExecution{
		File:    "./docker-compose.yml",
		Service: "web",
		Mode:    model.ComposeModeServices,
		Pull:    model.ComposePullMissing,
	}
	args := buildComposeArgs(ce, &model.Task{}, nil, "")
	assert.NotContains(t, strings.Join(args, " "), "--pull")
}

func TestComposeBackend_EnvFlags_DeterministicOrder(t *testing.T) {
	task := &model.Task{
		Env:     map[string]string{"FOO": "1", "BAR": "2"},
		Secrets: map[string]string{"ZED": "secret"},
	}
	flags := composeEnvFlags(task, nil, 0)
	// composeEnvFlags returns NAMES only (values go into the CLI env, not argv);
	// alphabetical ordering keeps test assertions stable.
	assert.Equal(t, []string{
		"BAR",
		"FOO",
		"RUNWISP_INSTANCE_INDEX",
		"ZED",
	}, flags)
}

func TestComposeBackend_EnvFlags_SecretOverridesEnv(t *testing.T) {
	task := &model.Task{
		Env:     map[string]string{"PASSWORD": "plain"},
		Secrets: map[string]string{"PASSWORD": "from-secret"},
	}
	// composeEnv carries the resolved KEY=VALUE pairs (injected into the CLI
	// env); it's where the secret-over-env precedence is observable.
	env := composeEnv(task, nil, 0)
	assert.Contains(t, env, "PASSWORD=from-secret")
	assert.NotContains(t, env, "PASSWORD=plain")
	// The value-less flags carry only the name.
	assert.Equal(t, []string{"PASSWORD", "RUNWISP_INSTANCE_INDEX"}, composeEnvFlags(task, nil, 0))
}

func TestComposeContainerName(t *testing.T) {
	tests := []struct {
		project, service string
		idx              int
		want             string
	}{
		{"myapp", "web", 0, "myapp_web_0"},
		{"myapp", "web", 3, "myapp_web_3"},
		{"", "web", 1, "web_1"},
		{"myapp", "", 1, "myapp_1"},
		{"", "", 0, ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, composeContainerName(tc.project, tc.service, tc.idx),
			"project=%q service=%q idx=%d", tc.project, tc.service, tc.idx)
	}
}

// TestComposeBackend_Start_RecordsArgs uses a PATH-shim fake `docker` to
// observe the args actually passed by Start() and verify the exit code
// surfaces. The shim writes its argv to a file the test then reads back.
func TestComposeBackend_Start_RecordsArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	installDockerShim(t, dir, argsFile, 0)

	b := &ComposeBackend{dockerCmd: "docker"}
	ce := &model.ComposeExecution{
		File:        "/tmp/dc.yml",
		ProjectName: "demo",
		Service:     "web",
		Mode:        model.ComposeModeServices,
	}
	task := &model.Task{Env: map[string]string{"FOO": "bar"}}
	run := &model.Run{InstanceIndex: 1}

	proc, err := b.Start(context.Background(), task, run, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	exit, _ := proc.Wait()
	assert.Equal(t, 0, exit)

	recorded, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	rec := string(recorded)
	assert.Contains(t, rec, "compose")
	assert.Contains(t, rec, "-f /tmp/dc.yml")
	assert.Contains(t, rec, "-p demo")
	assert.Contains(t, rec, "--name demo_web_1")
	// Only the value-less `-e KEY` flags reach argv; the values are injected
	// into the docker CLI's environment instead (see the env-injection test).
	assert.Contains(t, rec, "-e RUNWISP_INSTANCE_INDEX")
	assert.Contains(t, rec, "-e FOO")
	assert.NotContains(t, rec, "FOO=bar", "env values must never appear on argv")
	assert.NotContains(t, rec, "RUNWISP_INSTANCE_INDEX=1", "env values must never appear on argv")
}

// TestComposeBackend_Start_InjectsEnvIntoChildProcess pins the hardening: the
// actual env/secret values are delivered to docker through the CLI child's
// environment (so `-e KEY` can resolve them), never through argv. The shim
// dumps its own environment and the test asserts the secret value is present
// there.
func TestComposeBackend_Start_InjectsEnvIntoChildProcess(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"compose\" ] && [ \"$2\" = \"version\" ]; then\n" +
		"  echo 'Docker Compose v2.test'\n" +
		"  exit 0\n" +
		"fi\n" +
		"env > '" + envFile + "'\n" +
		"exit 0\n"
	installDockerShimScript(t, dir, body)

	b := &ComposeBackend{dockerCmd: "docker"}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", ProjectName: "demo", Service: "web", Mode: model.ComposeModeServices}
	task := &model.Task{Secrets: map[string]string{"API_TOKEN": "s3cr3t"}}

	proc, err := b.Start(context.Background(), task, &model.Run{InstanceIndex: 0}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	proc.Wait()

	recorded, err := os.ReadFile(envFile)
	require.NoError(t, err)
	env := string(recorded)
	assert.Contains(t, env, "API_TOKEN=s3cr3t",
		"secret value must reach the docker CLI via its environment, not argv")
	assert.Contains(t, env, "RUNWISP_INSTANCE_INDEX=0")
}

// TestComposeBackend_Start_ReclaimsStaleInstance verifies that, before
// launching, Start force-removes a container our prior daemon life left behind
// for this slot — the kill -9 / restart orphan that otherwise collides with
// the deterministic --name.
func TestComposeBackend_Start_ReclaimsStaleInstance(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	installRecordingDockerShim(t, dir, logFile, "stale123\n")

	b := &ComposeBackend{dockerCmd: "docker", fingerprint: "fp-test"}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", ProjectName: "demo", Service: "web", Mode: model.ComposeModeServices}
	task := &model.Task{Name: "boxes.web"}

	proc, err := b.Start(context.Background(), task, &model.Run{InstanceIndex: 0}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	proc.Wait()

	calls := readDockerCalls(t, logFile)
	assert.Contains(t, calls, "ps -aq --filter label=com.runwisp.task=boxes.web --filter label=com.runwisp.instance=0 --filter label=com.runwisp.instance-fp=fp-test",
		"reclaim must query our own labelled containers for this slot")
	assert.Contains(t, calls, "rm -f stale123", "the discovered orphan must be force-removed before launch")
}

// TestComposeBackend_Start_CleanupRemovesInstance verifies Process.Cleanup
// force-removes the instance container, covering the SIGKILL / graceful-stop
// overrun where `--rm` never fires.
func TestComposeBackend_Start_CleanupRemovesInstance(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	installRecordingDockerShim(t, dir, logFile, "live456\n")

	b := &ComposeBackend{dockerCmd: "docker", fingerprint: "fp-test"}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", ProjectName: "demo", Service: "web", Mode: model.ComposeModeServices}
	task := &model.Task{Name: "boxes.web"}

	proc, err := b.Start(context.Background(), task, &model.Run{InstanceIndex: 2}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	proc.Wait()

	require.NotNil(t, proc.Cleanup, "services-mode Process must carry a Cleanup")
	require.NoError(t, os.WriteFile(logFile, nil, 0644)) // isolate cleanup's calls
	proc.Cleanup()

	calls := readDockerCalls(t, logFile)
	assert.Contains(t, calls, "ps -aq --filter label=com.runwisp.task=boxes.web --filter label=com.runwisp.instance=2 --filter label=com.runwisp.instance-fp=fp-test")
	assert.Contains(t, calls, "rm -f live456")
}

// TestComposeBackend_Start_StackModeNoReclaimOrCleanup confirms stack mode
// neither reclaims nor sets a Cleanup — compose owns those container lifecycles.
func TestComposeBackend_Start_StackModeNoReclaimOrCleanup(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	installRecordingDockerShim(t, dir, logFile, "")

	b := &ComposeBackend{dockerCmd: "docker", fingerprint: "fp-test"}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", ProjectName: "demo", Mode: model.ComposeModeStack}

	proc, err := b.Start(context.Background(), &model.Task{Name: "boxes.stack"}, &model.Run{}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	proc.Wait()

	assert.Nil(t, proc.Cleanup, "stack mode lets compose own container cleanup")
	assert.NotContains(t, readDockerCalls(t, logFile), "ps -aq", "stack mode must not reclaim")
}

func TestComposeBackend_Start_RejectsWrongExecutionType(t *testing.T) {
	b := &ComposeBackend{dockerCmd: "docker"}
	_, err := b.Start(context.Background(), &model.Task{}, nil, &model.ShellExecution{Script: "noop"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-compose")
}

func TestComposeBackend_Start_RejectsEmptyFile(t *testing.T) {
	b := &ComposeBackend{dockerCmd: "docker"}
	_, err := b.Start(context.Background(), &model.Task{}, nil, &model.ComposeExecution{Service: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file")
}

func TestComposeBackend_Start_PropagatesExitCode(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	installDockerShim(t, dir, argsFile, 7)

	b := &ComposeBackend{dockerCmd: "docker"}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", Service: "web", Mode: model.ComposeModeServices}
	proc, err := b.Start(context.Background(), &model.Task{}, &model.Run{}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	exit, _ := proc.Wait()
	assert.Equal(t, 7, exit)
}

func TestComposeBackend_Available_TrueWhenShimPresent(t *testing.T) {
	dir := t.TempDir()
	installDockerShim(t, dir, filepath.Join(dir, "args.txt"), 0)

	b := &ComposeBackend{dockerCmd: "docker"}
	assert.True(t, b.Available(context.Background()))
}

func TestComposeBackend_Available_FalseWhenAbsent(t *testing.T) {
	// Empty PATH ensures `docker` is unfindable. Some shells still resolve
	// builtins, but `docker` isn't one, so the exec.LookPath inside
	// CommandContext will fail to start the process.
	t.Setenv("PATH", "")
	b := &ComposeBackend{dockerCmd: "docker-does-not-exist"}
	assert.False(t, b.Available(context.Background()))
}

// TestComposeBackend_ProcessGroupSIGTERMReapsChildren mirrors the ShellBackend
// process-group test: the shim spawns a long-lived child and waits on it.
// Cancelling the context must SIGTERM the whole group so both the docker shim
// and its child die within the graceful window — proving setpgid + the
// SIGTERM ladder in compose.go reach grandchildren the CLI may spawn.
func TestComposeBackend_ProcessGroupSIGTERMReapsChildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	body := "#!/bin/sh\n" +
		"sleep 30 &\n" +
		"echo $! > '" + pidFile + "'\n" +
		"wait\n"
	installDockerShimScript(t, dir, body)

	task := &model.Task{GracefulStop: 200 * time.Millisecond}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", Service: "web", Mode: model.ComposeModeServices}

	ctx, cancel := context.WithCancel(context.Background())
	b := &ComposeBackend{dockerCmd: "docker"}
	proc, err := b.Start(ctx, task, &model.Run{}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)

	var childPid int
	require.Eventually(t, func() bool {
		pid, ok := readPidFromFile(pidFile)
		if !ok {
			return false
		}
		childPid = pid
		return true
	}, time.Second, 20*time.Millisecond, "shim must record the child PID")

	cancel()
	proc.Wait()

	require.Eventually(t, func() bool {
		err := syscall.Kill(childPid, 0) // signal 0 = existence probe
		return errors.Is(err, syscall.ESRCH) || os.IsPermission(err)
	}, time.Second, 20*time.Millisecond, "child must be reaped after the group is signalled")
}

// TestComposeBackend_ImmediateKillWhenGracefulStopZero verifies graceful_stop=0
// sends SIGKILL to the group with no SIGTERM grace window (compose.go:85-87).
// The shim traps SIGTERM so only SIGKILL can end it.
func TestComposeBackend_ImmediateKillWhenGracefulStopZero(t *testing.T) {
	dir := t.TempDir()
	installDockerShimScript(t, dir, "#!/bin/sh\ntrap '' TERM\nsleep 30\n")

	task := &model.Task{GracefulStop: 0}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", Service: "web", Mode: model.ComposeModeServices}

	ctx, cancel := context.WithCancel(context.Background())
	b := &ComposeBackend{dockerCmd: "docker"}
	proc, err := b.Start(ctx, task, &model.Run{}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)

	time.Sleep(50 * time.Millisecond) // let the trap install
	cancel()

	done := make(chan struct{})
	go func() { proc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("graceful_stop=0 must SIGKILL immediately, not wait out the sleep")
	}
}

// TestComposeBackend_WorkingDirPropagates confirms ce.WorkingDir becomes the
// child's cwd (compose.go:65-67) by having the shim record its own $PWD.
func TestComposeBackend_WorkingDirPropagates(t *testing.T) {
	shimDir := t.TempDir()
	workDir := t.TempDir()
	pwdFile := filepath.Join(shimDir, "pwd.txt")
	installDockerShimScript(t, shimDir, "#!/bin/sh\npwd > '"+pwdFile+"'\nexit 0\n")

	ce := &model.ComposeExecution{
		File:       "/tmp/dc.yml",
		Service:    "web",
		Mode:       model.ComposeModeServices,
		WorkingDir: workDir,
	}
	b := &ComposeBackend{dockerCmd: "docker"}
	proc, err := b.Start(context.Background(), &model.Task{}, &model.Run{}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	proc.Wait()

	recorded, err := os.ReadFile(pwdFile)
	require.NoError(t, err)
	// macOS resolves TempDir through /private symlinks; compare the resolved
	// paths so the assertion holds on both Linux and macOS.
	wantResolved, err := filepath.EvalSymlinks(workDir)
	require.NoError(t, err)
	gotResolved, err := filepath.EvalSymlinks(strings.TrimSpace(string(recorded)))
	require.NoError(t, err)
	assert.Equal(t, wantResolved, gotResolved)
}

// TestComposeBackend_Start_ContextCancelledBeforeStart confirms a context that
// is already cancelled prevents the spawn rather than leaking a process.
func TestComposeBackend_Start_ContextCancelledBeforeStart(t *testing.T) {
	dir := t.TempDir()
	installDockerShim(t, dir, filepath.Join(dir, "args.txt"), 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &ComposeBackend{dockerCmd: "docker"}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", Service: "web", Mode: model.ComposeModeServices}
	_, err := b.Start(ctx, &model.Task{}, &model.Run{}, ce)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start docker compose")
}

// TestLazyComposeBackend_ReprobesAfterTransientFailure pins the H5 fix: a
// transient first-probe failure (docker still coming up) must not disable
// compose for the daemon's whole lifetime. The old code latched probed=true on
// the first failure and never retried. The shim fails `compose version` once,
// then succeeds — so the second ensureProbed must report available.
func TestLazyComposeBackend_ReprobesAfterTransientFailure(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "probe.count")
	body := "#!/bin/sh\n" +
		"if [ \"$1\" = \"compose\" ] && [ \"$2\" = \"version\" ]; then\n" +
		"  n=$(cat '" + counter + "' 2>/dev/null || echo 0)\n" +
		"  n=$((n+1))\n" +
		"  echo $n > '" + counter + "'\n" +
		"  if [ \"$n\" = \"1\" ]; then\n" +
		"    exit 1\n" + // first probe: docker not up yet
		"  fi\n" +
		"  echo 'Docker Compose v2.test'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	installDockerShimScript(t, dir, body)

	l := NewLazyComposeBackend("fp-test")

	_, availFirst := l.ensureProbed(context.Background())
	assert.False(t, availFirst, "the transient first-probe failure must report unavailable")

	_, availSecond := l.ensureProbed(context.Background())
	assert.True(t, availSecond, "compose must be re-probed and become available once docker is up")

	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	assert.Equal(t, "2", strings.TrimSpace(string(data)),
		"ensureProbed must actually re-run the probe after a failure, not latch the failed result")
}

func TestLazyComposeBackend_ReturnsErrorWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	l := NewLazyComposeBackend("fp-test")
	_, err := l.Start(context.Background(), &model.Task{}, &model.Run{},
		&model.ComposeExecution{File: "/tmp/dc.yml", Service: "web", Mode: model.ComposeModeServices})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker compose unavailable")
}

// installDockerShim drops a minimal shell script named `docker` into shimDir,
// makes it executable, prepends shimDir to PATH, and arranges for it to
// record its full argv into argsFile and exit with exitCode. Tests use it to
// observe the exact arguments ComposeBackend passes to docker compose.
func installDockerShim(t *testing.T, shimDir, argsFile string, exitCode int) {
	t.Helper()
	body := "#!/bin/sh\n" +
		"echo \"$@\" > '" + argsFile + "'\n" +
		"if [ \"$1\" = \"compose\" ] && [ \"$2\" = \"version\" ]; then\n" +
		"  echo 'Docker Compose v2.test'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	installDockerShimScript(t, shimDir, body)
}

// installDockerShimScript installs a custom-bodied `docker` shim so kill-ladder
// and working-dir tests can supply their own trap/sleep behavior. The body must
// still answer `docker compose version` for the availability probe, or callers
// must avoid triggering it. PATH is prepended with shimDir for the test's
// lifetime.
func installDockerShimScript(t *testing.T, shimDir, body string) {
	t.Helper()
	shim := filepath.Join(shimDir, "docker")
	require.NoError(t, os.WriteFile(shim, []byte(body), 0755))
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Sanity-check the shim is actually resolvable on PATH.
	_, err := exec.LookPath("docker")
	require.NoError(t, err, "docker shim should resolve via PATH")
}

// installRecordingDockerShim drops a `docker` shim that appends every
// invocation's argv (one line each) to logFile, answers the availability probe,
// and prints psOutput on `docker ps ...` so reclaim/cleanup can discover IDs.
// Unlike installDockerShim (which overwrites a single args file) this preserves
// the full call sequence so tests can assert reclaim/cleanup ran.
func installRecordingDockerShim(t *testing.T, shimDir, logFile, psOutput string) {
	t.Helper()
	body := "#!/bin/sh\n" +
		"echo \"$@\" >> '" + logFile + "'\n" +
		"if [ \"$1\" = \"compose\" ] && [ \"$2\" = \"version\" ]; then\n" +
		"  echo 'Docker Compose v2.test'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"ps\" ]; then\n" +
		"  printf '%s' '" + psOutput + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	installDockerShimScript(t, shimDir, body)
}

// readDockerCalls returns the recorded shim invocations as a newline-joined
// string for substring assertions. A missing log file means no calls.
func readDockerCalls(t *testing.T, logFile string) string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	require.NoError(t, err)
	return string(data)
}

func drain(r io.ReadCloser) {
	if r == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r)
}
