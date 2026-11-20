// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sync"
	"syscall"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// containerNameConflict matches the Docker CLI's error when `docker run
// --name` finds the name taken, capturing the name and the holder's ID.
var containerNameConflict = regexp.MustCompile(`The container name "/?([^"]+)" is already in use by container "([0-9a-f]{12,64})"`)

// conflictExitCode is what `docker run` exits with when Docker itself fails,
// a name conflict included.
const conflictExitCode = 125

// stderrTailSize is how much of the end of stderr is kept to find Docker's
// error in.
const stderrTailSize = 4096

// adoptNameConflict wraps a service's shell run so a `docker run --name` that
// finds its container name held by a container RunWisp didn't start (someone
// restarted it by hand, or the daemon was killed while it ran) carries on
// with that container instead of failing in a restart loop. A running holder
// is attached to: its output becomes the run's, and the run ends, stopping
// and removing it, like the service's own container would. A stopped holder
// is removed and the command runs again. Either happens once per run, in the
// same run, so the takeover shows in its log.
func adoptNameConflict(ctx context.Context, task *model.Task, first *Process, rerun func() (*Process, error)) *Process {
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	notices := make(chan string, 1)
	type exit struct {
		code int
		err  error
	}
	done := make(chan exit, 1)

	var mu sync.Mutex
	cur := first
	current := func() *Process {
		mu.Lock()
		defer mu.Unlock()
		return cur
	}

	go func() {
		code, tail, err := forwardProcess(first, stdoutW, stderrW)
		if code == conflictExitCode && ctx.Err() == nil {
			if next, msg := adoptConflictHolder(ctx, task, tail, rerun); next != nil {
				notices <- msg
				mu.Lock()
				cur = next
				mu.Unlock()
				code, _, err = forwardProcess(next, stdoutW, stderrW)
			}
		}
		close(notices)
		_ = stdoutW.Close()
		_ = stderrW.Close()
		done <- exit{code, err}
	}()

	return &Process{
		Stdout:  stdoutR,
		Stderr:  stderrR,
		Notices: notices,
		Wait: func() (int, error) {
			e := <-done
			return e.code, e.err
		},
		ForceKill: func() {
			if p := current(); p.ForceKill != nil {
				p.ForceKill()
			}
		},
		// ponytail: usage is sampled for the first process only; a rerun or an
		// attached container goes unmeasured.
		Pid: first.Pid,
		Rusage: func() *syscall.Rusage {
			if p := current(); p.Rusage != nil {
				return p.Rusage()
			}
			return nil
		},
	}
}

// adoptConflictHolder takes over the container holding the name, when stderr
// ends in Docker's name-conflict error. It returns the process that continues
// the run and the system line saying so, or nil when there is nothing to take
// over or Docker can't say what holds the name.
func adoptConflictHolder(ctx context.Context, task *model.Task, stderrTail string, rerun func() (*Process, error)) (*Process, string) {
	m := containerNameConflict.FindStringSubmatch(stderrTail)
	if m == nil {
		return nil, ""
	}
	name, id := m[1], m[2]
	running, _, err := containerState("docker", id)
	if err != nil {
		return nil, ""
	}
	if running {
		proc, err := attachContainer(ctx, "docker", task, id, true)
		if err != nil {
			return nil, ""
		}
		return proc, fmt.Sprintf("Container %s, which has this service's name, was started outside RunWisp. "+
			"Attached to it: its output follows, and a stop or restart removes it and starts the service's own.", name)
	}
	if !removeContainer("docker", id) {
		return nil, ""
	}
	proc, err := rerun()
	if err != nil {
		return nil, ""
	}
	return proc, fmt.Sprintf("Removed stopped container %s, which held this service's name, and started the command again.", name)
}

// forwardProcess copies p's output into stdout and stderr until both end,
// then waits for it. It returns the exit and the end of stderr.
func forwardProcess(p *Process, stdout, stderr io.Writer) (code int, stderrTail string, err error) {
	tail := &tailWriter{}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stdout, p.Stdout)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.MultiWriter(stderr, tail), p.Stderr)
	}()
	wg.Wait()
	code, err = p.Wait()
	if p.Cleanup != nil {
		p.Cleanup()
	}
	return code, string(tail.b), err
}

// tailWriter keeps the last stderrTailSize bytes written to it.
type tailWriter struct{ b []byte }

func (t *tailWriter) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > stderrTailSize {
		t.b = append(t.b[:0], t.b[len(t.b)-stderrTailSize:]...)
	}
	return len(p), nil
}
