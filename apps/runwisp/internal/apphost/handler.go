// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apphost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// handlerRun is one run an app is serving. Its pipes feed the executor, which
// writes the log, applies log caps and redaction, and publishes the lines.
type handlerRun struct {
	stdout, stderr *io.PipeWriter
	done           chan struct{}
	once           sync.Once
	code           int
}

func (r *handlerRun) finish(code int) {
	r.once.Do(func() {
		r.code = code
		_ = r.stdout.Close()
		_ = r.stderr.Close()
		close(r.done)
	})
}

// Start sends a handler run to the active app. Everything else a run gets
// (timeout, retries, overlap, logs, notifications) is the executor's and the
// run manager's, same as for a shell run.
//
// Stopping a run asks the app to stop it and waits up to the unit's
// graceful_stop for its exit. A handler that ignores that is past the
// daemon's reach, so the run ends there and its later output is dropped.
func (h *Host) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*executor.Process, error) {
	if _, ok := def.(*model.SDKExecution); !ok {
		return nil, fmt.Errorf("apphost received non-sdk execution: %s", def.ExecType())
	}
	if run == nil {
		return nil, errors.New("sdk runs need a run record")
	}
	s := h.active(ctx.Done())
	if s == nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("no app is connected to run %q", task.Name)
	}

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	r := &handlerRun{stdout: stdoutW, stderr: stderrW, done: make(chan struct{})}
	err := s.begin(r, message{
		Type: "run",
		Run:  run.ID,
		Task: task.Name,
		// What a shell run would add; the app's own environment stays the app's.
		Env:    task.EnvOverlay(run.Params),
		Params: run.Params,
	})
	if err != nil {
		return nil, err
	}

	go h.stopOnCancel(ctx, s, run.ID, r, task)

	return &executor.Process{
		Stdout: stdoutR,
		Stderr: stderrR,
		Wait: func() (int, error) {
			<-r.done
			s.forget(run.ID)
			return r.code, nil
		},
		ForceKill: func() { r.finish(-1) },
	}, nil
}

// stopOnCancel asks the app to stop the run once its context ends (a stop,
// timeout, overlap kill, or shutdown), then gives up on it after
// graceful_stop.
func (h *Host) stopOnCancel(ctx context.Context, s *session, id string, r *handlerRun, task *model.Task) {
	select {
	case <-r.done:
		return
	case <-ctx.Done():
	}
	if err := s.send(message{Type: "stop", Run: id}); err != nil {
		r.finish(-1)
		return
	}
	select {
	case <-r.done:
	case <-h.after(task.GracefulStopValue()):
		_, _ = io.WriteString(r.stderr, "runwisp: the run did not stop within graceful_stop\n")
		r.finish(-1)
	}
}
