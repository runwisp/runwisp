// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package executor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

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
		Env: map[string]string{"LOG_LEVEL": "info"},
	}
	run := &model.Run{InstanceIndex: 2}

	args := buildComposeArgs(ce, task, run)
	joined := strings.Join(args, " ")

	assert.Contains(t, joined, "compose -f ./docker-compose.yml")
	assert.Contains(t, joined, "-p myapp")
	assert.Contains(t, joined, "--profile prod")
	assert.Contains(t, joined, "--env-file ./.env")
	assert.Contains(t, joined, "run --rm --service-ports --use-aliases")
	assert.Contains(t, joined, "--no-deps")
	assert.Contains(t, joined, "--pull always")
	assert.Contains(t, joined, "--name myapp_web_2")
	assert.Contains(t, joined, "-e RUNWISP_INSTANCE_INDEX=2")
	assert.Contains(t, joined, "-e LOG_LEVEL=info")
	// service name is the final positional argument
	assert.Equal(t, "web", args[len(args)-1])
}

func TestComposeBackend_BuildArgs_StackMode(t *testing.T) {
	ce := &model.ComposeExecution{
		File:        "./docker-compose.yml",
		ProjectName: "myapp",
		Mode:        model.ComposeModeStack,
	}
	args := buildComposeArgs(ce, &model.Task{}, nil)
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
	args := buildComposeArgs(ce, &model.Task{}, nil)
	assert.NotContains(t, strings.Join(args, " "), "--no-deps")
}

func TestComposeBackend_BuildArgs_OmitsPullWhenMissing(t *testing.T) {
	ce := &model.ComposeExecution{
		File:    "./docker-compose.yml",
		Service: "web",
		Mode:    model.ComposeModeServices,
		Pull:    model.ComposePullMissing,
	}
	args := buildComposeArgs(ce, &model.Task{}, nil)
	assert.NotContains(t, strings.Join(args, " "), "--pull")
}

func TestComposeBackend_EnvFlags_DeterministicOrder(t *testing.T) {
	task := &model.Task{
		Env:       map[string]string{"FOO": "1", "BAR": "2"},
		SecretEnv: map[string]string{"ZED": "secret"},
	}
	flags := composeEnvFlags(task, 0)
	// alphabetical ordering keeps test assertions stable
	assert.Equal(t, []string{
		"BAR=2",
		"FOO=1",
		"RUNWISP_INSTANCE_INDEX=0",
		"ZED=secret",
	}, flags)
}

func TestComposeBackend_EnvFlags_SecretOverridesEnv(t *testing.T) {
	task := &model.Task{
		Env:       map[string]string{"PASSWORD": "plain"},
		SecretEnv: map[string]string{"PASSWORD": "from-secret"},
	}
	flags := composeEnvFlags(task, 0)
	assert.Contains(t, flags, "PASSWORD=from-secret")
	assert.NotContains(t, flags, "PASSWORD=plain")
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
	if runtime.GOOS == "windows" {
		t.Skip("PATH-shim depends on POSIX shell")
	}
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
	assert.Contains(t, rec, "RUNWISP_INSTANCE_INDEX=1")
	assert.Contains(t, rec, "FOO=bar")
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

func TestLazyComposeBackend_ReturnsErrorWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	l := NewLazyComposeBackend()
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
	shim := filepath.Join(shimDir, "docker")
	body := "#!/bin/sh\n" +
		"echo \"$@\" > '" + argsFile + "'\n" +
		"if [ \"$1\" = \"compose\" ] && [ \"$2\" = \"version\" ]; then\n" +
		"  echo 'Docker Compose v2.test'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	require.NoError(t, os.WriteFile(shim, []byte(body), 0755))
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Sanity-check the shim is actually resolvable on PATH.
	_, err := exec.LookPath("docker")
	require.NoError(t, err, "docker shim should resolve via PATH")
}

func drain(r io.ReadCloser) {
	if r == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r)
}
