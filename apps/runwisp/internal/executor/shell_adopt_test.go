// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package executor

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const holderID = "4d1f2e3c4b5a"

// nameConflictShim is a docker shim whose `docker run --name slackbot` finds
// the name held by container holderID until `docker rm` frees it. The holder
// reports holderState ("true 0" running, "false <code>" exited) until its
// logs end (it then exited 7) or `docker stop` (143). logs follows with
// logsBody.
func nameConflictShim(t *testing.T, holderState, logsBody string) string {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	stopped := filepath.Join(dir, "stopped")
	freed := filepath.Join(dir, "freed")
	exited := filepath.Join(dir, "exited")
	installDockerShimScript(t, dir, "#!/bin/sh\n"+
		"echo \"$@\" >> '"+logFile+"'\n"+
		"case \"$1\" in\n"+
		"run) if [ -e '"+freed+"' ]; then echo 'own container'; exit 0; fi\n"+
		"  echo 'docker: Error response from daemon: Conflict. The container name \"/slackbot\" is already in use by container \""+holderID+"\". You have to remove (or rename) that container to be able to reuse that name.' >&2; exit 125 ;;\n"+
		"inspect) if [ -e '"+stopped+"' ]; then echo 'false 143'; elif [ -e '"+exited+"' ]; then echo 'false 7'; else echo '"+holderState+"'; fi ;;\n"+
		"logs) "+logsBody+"; touch '"+exited+"' ;;\n"+
		"stop) touch '"+stopped+"' ;;\n"+
		"rm) touch '"+freed+"' ;;\n"+
		"esac\n")
	return logFile
}

func slackbot(kind model.TaskKind) (*model.Task, *model.ShellExecution) {
	task := &model.Task{Name: "slackbot", Kind: kind, GracefulStop: durPtr(2 * time.Second)}
	return task, &model.ShellExecution{Script: "docker run --rm --name slackbot slackbot:latest"}
}

type shellRun struct {
	stdout, stderr string
	notices        []string
	exit           int
}

// startShell starts def and drains its output at once, as the executor does.
// The returned func waits for the run to end.
func startShell(t *testing.T, ctx context.Context, task *model.Task, def *model.ShellExecution) (*Process, func() shellRun) {
	t.Helper()
	proc, err := (&ShellBackend{}).Start(ctx, task, &model.Run{ID: "RUN1"}, def)
	require.NoError(t, err)
	var r shellRun
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		b, _ := io.ReadAll(proc.Stdout)
		r.stdout = string(b)
	}()
	go func() {
		defer wg.Done()
		b, _ := io.ReadAll(proc.Stderr)
		r.stderr = string(b)
	}()
	return proc, func() shellRun {
		if proc.Notices != nil {
			for n := range proc.Notices {
				r.notices = append(r.notices, n)
			}
		}
		wg.Wait()
		r.exit, _ = proc.Wait()
		return r
	}
}

// A service whose container someone restarted by hand attaches to that
// container in the same run instead of failing with exit 125 in a loop. The
// run ends with the container's exit code, and the container is removed so
// the service's next start gets its name back.
func TestShellBackend_Service_AttachesToContainerHoldingName(t *testing.T) {
	logFile := nameConflictShim(t, "true 0", "echo 'hello from holder'")
	task, def := slackbot(model.KindService)

	_, wait := startShell(t, context.Background(), task, def)
	r := wait()

	assert.Equal(t, 7, r.exit)
	assert.Contains(t, r.stderr, "is already in use", "Docker's own error stays in the log")
	assert.Equal(t, "hello from holder\n", r.stdout)
	require.Len(t, r.notices, 1)
	assert.Contains(t, r.notices[0], "Container slackbot")
	calls := readDockerCalls(t, logFile)
	assert.Regexp(t, `logs -f --since \S+ `+holderID, calls)
	assert.Contains(t, calls, "rm "+holderID)
}

// Stopping the service while attached stops and removes the container.
func TestShellBackend_Service_StopWhileAttachedRemovesContainer(t *testing.T) {
	logFile := nameConflictShim(t, "true 0", "sleep 30")
	task, def := slackbot(model.KindService)

	ctx, cancel := context.WithCancel(context.Background())
	_, wait := startShell(t, ctx, task, def)
	require.Eventually(t, func() bool {
		return strings.Contains(readDockerCalls(t, logFile), "logs -f")
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	r := wait()

	assert.Equal(t, 143, r.exit)
	calls := readDockerCalls(t, logFile)
	assert.Contains(t, calls, "stop -t 2 "+holderID)
	assert.Contains(t, calls, "rm "+holderID)
}

// A stopped container holding the name is removed and the command runs again.
func TestShellBackend_Service_RemovesStoppedHolderAndReruns(t *testing.T) {
	logFile := nameConflictShim(t, "false 0", "exit 0")
	task, def := slackbot(model.KindService)

	_, wait := startShell(t, context.Background(), task, def)
	r := wait()

	assert.Equal(t, 0, r.exit)
	assert.Equal(t, "own container\n", r.stdout)
	require.Len(t, r.notices, 1)
	assert.Contains(t, r.notices[0], "Removed stopped container slackbot")
	assert.NotContains(t, readDockerCalls(t, logFile), "logs -f")
}

// A scheduled task is left alone: there a name conflict usually means the
// previous run is still going.
func TestShellBackend_Task_DoesNotAdoptContainer(t *testing.T) {
	logFile := nameConflictShim(t, "true 0", "exit 0")
	task, def := slackbot(model.KindTask)

	proc, wait := startShell(t, context.Background(), task, def)
	r := wait()

	assert.Equal(t, 125, r.exit)
	assert.Nil(t, proc.Notices)
	assert.NotContains(t, readDockerCalls(t, logFile), "inspect")
}

// conflictStderr is Docker's name-conflict error as `docker run` prints it.
const conflictStderr = `docker: Error response from daemon: Conflict. The container name "/slackbot" is already in use by container "` + holderID + `".`

func TestAdoptConflictHolder_LeavesRunAloneWhenItCannotTakeOver(t *testing.T) {
	rerunFails := func() (*Process, error) { return nil, io.ErrUnexpectedEOF }
	for name, tc := range map[string]struct {
		shell  string
		stderr string
		rerun  func() (*Process, error)
	}{
		"not a name conflict": {"#!/bin/sh\nexit 0\n", "docker: no such image", nil},
		"inspect fails":       {"#!/bin/sh\nexit 1\n", conflictStderr, nil},
		"remove fails":        {"#!/bin/sh\ncase \"$1\" in inspect) echo 'false 0' ;; rm) exit 1 ;; esac\n", conflictStderr, nil},
		"rerun fails":         {"#!/bin/sh\ncase \"$1\" in inspect) echo 'false 0' ;; esac\n", conflictStderr, rerunFails},
	} {
		t.Run(name, func(t *testing.T) {
			installDockerShimScript(t, t.TempDir(), tc.shell)
			task, _ := slackbot(model.KindService)

			proc, msg := adoptConflictHolder(context.Background(), task, tc.stderr, tc.rerun)

			assert.Nil(t, proc)
			assert.Empty(t, msg)
		})
	}
}

// The wrapper hands ForceKill and Rusage to the process that is current, and
// keeps the first process's pid.
func TestAdoptNameConflict_DelegatesToCurrentProcess(t *testing.T) {
	var killed bool
	rusage := &syscall.Rusage{}
	first := &Process{
		Pid:       42,
		Stdout:    io.NopCloser(strings.NewReader("")),
		Stderr:    io.NopCloser(strings.NewReader("")),
		Wait:      func() (int, error) { return 0, nil },
		ForceKill: func() { killed = true },
		Rusage:    func() *syscall.Rusage { return rusage },
	}
	task, _ := slackbot(model.KindService)

	proc := adoptNameConflict(context.Background(), task, first, nil)
	code, err := proc.Wait()
	proc.ForceKill()

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.True(t, killed)
	assert.Equal(t, 42, proc.Pid)
	assert.Same(t, rusage, proc.Rusage())
}

func TestForwardProcess_RunsCleanupAndKeepsStderrTail(t *testing.T) {
	var cleaned bool
	p := &Process{
		Stdout:  io.NopCloser(strings.NewReader("out")),
		Stderr:  io.NopCloser(strings.NewReader(strings.Repeat("x", stderrTailSize) + "end")),
		Wait:    func() (int, error) { return 3, nil },
		Cleanup: func() { cleaned = true },
	}
	var stdout, stderr strings.Builder

	code, tail, err := forwardProcess(p, &stdout, &stderr)

	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.True(t, cleaned)
	assert.Equal(t, "out", stdout.String())
	assert.Len(t, stderr.String(), stderrTailSize+3)
	assert.Len(t, tail, stderrTailSize)
	assert.True(t, strings.HasSuffix(tail, "end"))
}
