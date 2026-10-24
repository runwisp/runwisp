// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package executor

import (
	"context"
	"io"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// composeUpShim is a docker shim for the attach path. It records every call
// in the returned log file, answers the compose-up probe with upPS (exiting
// upPSExit), follows logs with logsBody, and reports the container as exited
// exitedCode, or, when that is empty, as running until `docker stop` drops the
// stopped marker, then as exited stoppedCode.
func composeUpShim(t *testing.T, dir, upPS string, upPSExit int, logsBody, exitedCode, stoppedCode string) string {
	t.Helper()
	logFile := filepath.Join(dir, "calls.log")
	stopped := filepath.Join(dir, "stopped")
	body := "#!/bin/sh\n" +
		"echo \"$@\" >> '" + logFile + "'\n" +
		"case \"$1\" in\n" +
		"compose) exit 0 ;;\n" +
		"ps) case \"$*\" in *oneoff=False*) printf '%s' '" + upPS + "'; exit " + strconv.Itoa(upPSExit) + " ;; esac; exit 0 ;;\n" +
		"logs) " + logsBody + " ;;\n" +
		"stop) touch '" + stopped + "'; exit 0 ;;\n" +
		"inspect) if [ -e '" + stopped + "' ]; then echo 'false " + stoppedCode + "'; " +
		"elif [ -n '" + exitedCode + "' ]; then echo 'false " + exitedCode + "'; else echo 'true 0'; fi; exit 0 ;;\n" +
		"esac\n" +
		"exit 0\n"
	installDockerShimScript(t, dir, body)
	return logFile
}

func composeUpService() (*model.Task, *model.ComposeExecution) {
	task := &model.Task{Name: "blog.web", Kind: model.KindService, GracefulStop: durPtr(2 * time.Second)}
	ce := &model.ComposeExecution{File: "/tmp/dc.yml", ProjectName: "blog", Service: "web", Mode: model.ComposeModeRun}
	return task, ce
}

// A compose block added over a running `docker compose up` stack must not
// start a second container next to the first: the run follows the existing
// one and ends with its exit code.
func TestComposeBackend_Start_AttachesToComposeUpContainer(t *testing.T) {
	dir := t.TempDir()
	logFile := composeUpShim(t, dir, "abc123 blog-web-1\n", 0, "echo 'hello from up'; exit 0", "3", "")
	task, ce := composeUpService()

	b := &ComposeBackend{dockerCmd: "docker", fingerprint: "fp-test"}
	proc, err := b.Start(context.Background(), task, &model.Run{ID: "RUN1"}, ce)
	require.NoError(t, err)
	out, _ := io.ReadAll(proc.Stdout)
	drain(proc.Stderr)
	exit, err := proc.Wait()

	require.NoError(t, err)
	assert.Equal(t, 3, exit, "the run reports the container's own exit code")
	assert.Equal(t, "hello from up\n", string(out))
	assert.Contains(t, proc.Notice, "Attached to blog-web-1")
	calls := readDockerCalls(t, logFile)
	assert.Contains(t, calls, "ps --filter label=com.docker.compose.project=blog --filter label=com.docker.compose.service=web --filter label=com.docker.compose.oneoff=False")
	assert.Regexp(t, `logs -f --since \S+ abc123`, calls)
	assert.NotContains(t, calls, "run --rm", "no second container")
	assert.NotContains(t, calls, "stop -t", "a container that exited on its own is not stopped")
}

// Stopping or restarting an attached run stops the `up` container, so the next
// start runs RunWisp's own and the two never run side by side.
func TestComposeBackend_Start_AttachedStopStopsContainer(t *testing.T) {
	dir := t.TempDir()
	logFile := composeUpShim(t, dir, "abc123 blog-web-1\n", 0, "exec sleep 30", "", "143")
	task, ce := composeUpService()

	ctx, cancel := context.WithCancel(context.Background())
	b := &ComposeBackend{dockerCmd: "docker", fingerprint: "fp-test"}
	proc, err := b.Start(ctx, task, &model.Run{ID: "RUN1"}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	cancel()
	exit, err := proc.Wait()

	require.NoError(t, err)
	assert.Equal(t, 143, exit)
	assert.Contains(t, readDockerCalls(t, logFile), "stop -t 2 abc123")
}

// Only services attach. A task's run is a one-off job, not the long-lived
// container `up` keeps, so it runs its own container as before.
func TestComposeBackend_Start_TaskIgnoresComposeUpContainers(t *testing.T) {
	dir := t.TempDir()
	logFile := composeUpShim(t, dir, "abc123 blog-web-1\n", 0, "exit 0", "0", "")
	_, ce := composeUpService()

	b := &ComposeBackend{dockerCmd: "docker", fingerprint: "fp-test"}
	proc, err := b.Start(context.Background(), &model.Task{Name: "blog.migrate"}, &model.Run{ID: "RUN1"}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	proc.Wait()

	calls := readDockerCalls(t, logFile)
	assert.NotContains(t, calls, "oneoff=False")
	assert.Contains(t, calls, "run --rm")
	assert.Empty(t, proc.Notice)
}

func TestComposeBackend_Start_ComposeUpProbeErrorFailsOpen(t *testing.T) {
	dir := t.TempDir()
	logFile := composeUpShim(t, dir, "", 1, "exit 0", "0", "")
	task, ce := composeUpService()

	b := &ComposeBackend{dockerCmd: "docker", fingerprint: "fp-test"}
	proc, err := b.Start(context.Background(), task, &model.Run{ID: "RUN1"}, ce)
	require.NoError(t, err)
	go drain(proc.Stdout)
	go drain(proc.Stderr)
	proc.Wait()

	calls := readDockerCalls(t, logFile)
	assert.Contains(t, calls, "run --rm", "a failed probe starts the managed container as before")
	assert.NotContains(t, calls, "logs -f")
}
