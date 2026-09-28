// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/runwisp/runwisp/internal/model"
)

// RunWatcher observes one live run for the whole time its process is up. The
// executor calls it on its own goroutine once the process has started, cancels
// ctx once the process has exited (or the run was stopped or timed out), and
// waits for it to return before closing the run's log — so everything a watcher
// does through ctl lands inside the run it watched. The runtime uses it for
// service health checks; the executor itself knows nothing about them.
type RunWatcher func(ctx context.Context, task *model.Task, run *model.Run, ctl RunControl)

// RunControl is what a RunWatcher may do to the run it watches.
type RunControl interface {
	// Probe runs probe's command to completion next to the run — same
	// instance, bounded by probe's timeout — without a run row, log file, or
	// events of its own, and reports how it ended.
	Probe(ctx context.Context, probe *model.Task) ProbeResult
	// Annotate appends a SYSTEM line to the run's log and live stream.
	Annotate(msg string)
	// Kill stops the run and records reason as its end reason. The first
	// reason wins; a Kill after the run is already ending is ignored.
	Kill(reason model.EndReason)
}

// ProbeResult is how a RunControl.Probe execution ended, plus the tail of what
// it printed (secrets redacted), for the operator to see why it failed.
type ProbeResult struct {
	ExecuteResult
	Output string
}

// probeOutputTail caps how much of a probe's output ProbeResult keeps: enough
// for an error message, bounded so a chatty probe can't bloat the run's log.
const probeOutputTail = 512

// runKiller cancels a run on behalf of a policy — log_on_full = "kill", a
// health check — and remembers why, so the result records that reason rather
// than `stopped`. The first reason wins. Kills are refused once ctx is already
// done (an operator stop or timeout got there first and keeps its reason) or
// once the process has exited (seal), so a policy can never relabel a run that
// ended on its own.
type runKiller struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	reason model.EndReason
	sealed bool
}

func newRunKiller(parent context.Context) *runKiller {
	ctx, cancel := context.WithCancel(parent)
	return &runKiller{ctx: ctx, cancel: cancel}
}

func (k *runKiller) kill(reason model.EndReason) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.sealed || k.reason != "" || k.ctx.Err() != nil {
		return
	}
	k.reason = reason
	k.cancel()
}

// seal refuses every later kill and returns the recorded reason, if any.
func (k *runKiller) seal() model.EndReason {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sealed = true
	return k.reason
}

// runControl is the RunControl handed to the RunWatcher of one run.
type runControl struct {
	r      *RoutingExecutor
	task   *model.Task
	run    *model.Run
	writer *LogWriter
	killer *runKiller
	redact *secretRedactor
}

func (c *runControl) Probe(ctx context.Context, probe *model.Task) ProbeResult {
	return c.r.probe(ctx, probe, c.run)
}

func (c *runControl) Annotate(msg string) {
	c.r.systemLine(c.writer, c.task, c.run, c.redact.text(msg))
}

func (c *runControl) Kill(reason model.EndReason) { c.killer.kill(reason) }

// startWatcher runs the RunWatcher, if any, for a run whose process has just
// started. The returned stop cancels it and blocks until it has returned; the
// caller invokes it once the process has exited, before the log closes.
func (r *RoutingExecutor) startWatcher(task *model.Task, run *model.Run, writer *LogWriter, killer *runKiller) (stop func()) {
	if r.watcher == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(killer.ctx)
	ctl := &runControl{r: r, task: task, run: run, writer: writer, killer: killer, redact: newSecretRedactor(task.Secrets)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("Recovered from panic in run watcher", "task", task.Name, "run", run.ID, "err", rec)
			}
		}()
		r.watcher(ctx, task, run, ctl)
	}()
	return func() {
		cancel()
		<-done
	}
}

// probe executes probe through the backend its definition resolves to, the
// same Backend.Start a run goes through, but keeps only a capped output tail
// in memory instead of a log file. run is the watched run: it carries the
// instance index a compose exec probe targets.
func (r *RoutingExecutor) probe(ctx context.Context, probe *model.Task, run *model.Run) ProbeResult {
	execDef := probe.ResolvedExecutionDef()
	if execDef == nil {
		return probeError(errors.New("missing execution definition"))
	}
	backend, ok := r.backends[execDef.ExecType()]
	if !ok {
		return probeError(fmt.Errorf("unsupported execution type: %s", execDef.ExecType()))
	}

	ctx, cancel := probe.WithTimeout(ctx)
	defer cancel()
	proc, err := backend.Start(ctx, probe, run, execDef)
	if err != nil {
		return probeError(fmt.Errorf("failed to start %s execution: %w", execDef.ExecType(), err))
	}

	tail := &tailBuffer{max: probeOutputTail}
	var wg sync.WaitGroup
	for _, stream := range []io.Reader{proc.Stdout, proc.Stderr} {
		if stream == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(tail, stream)
		}()
	}
	wg.Wait()
	exitCode, waitErr := proc.Wait()
	if proc.Cleanup != nil {
		proc.Cleanup()
	}

	return ProbeResult{
		ExecuteResult: *classifyExecuteResult(ctx, "", exitCode, waitErr),
		Output:        newSecretRedactor(probe.Secrets).text(tail.String()),
	}
}

func probeError(err error) ProbeResult {
	return ProbeResult{ExecuteResult: ExecuteResult{ExitCode: -1, Error: err}}
}

// tailBuffer is an io.Writer that keeps only the last max bytes written to
// it. Safe for concurrent writers (a probe's stdout and stderr). The cut may
// split a multi-byte character, so String drops any invalid remnant.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.buf), "")
}
