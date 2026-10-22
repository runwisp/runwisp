// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"io"
	"syscall"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// Backend executes a specific task execution type. Start launches the work and
// returns a Process whose streams the executor drains and whose Wait reports
// the exit code (0 = success).
//
// task carries the resolved task definition, including runtime knobs like
// GracefulStop that backends use to wire the SIGTERM→wait→SIGKILL ladder.
// run carries per-execution state: most backends ignore it, but service
// backends use run.InstanceIndex to differentiate concurrent instances.
type Backend interface {
	Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error)
}

// Process represents a running execution whose output can be streamed.
type Process struct {
	Stdout  io.ReadCloser // main output stream
	Stderr  io.ReadCloser // error stream (nil when not applicable)
	Wait    func() (exitCode int, err error)
	Cleanup func() // optional: clean up temp resources after Wait
	// ExitHint, when non-nil, is called after a non-zero exit the run did not
	// cause itself (no timeout, stop, or kill) and may return a system log line
	// explaining it. "" adds nothing.
	ExitHint func() string
	// Notice, when set, is a system log line the executor writes as soon as the
	// process has started: the backend saying it did something other than the
	// usual (compose attaching to a container it didn't start).
	Notice string
	// EndReason, when non-nil, is called after Wait and may return the
	// reason the backend itself ended the run for (an SDK app skipping a run
	// under on_overlap = "skip"). "" leaves the exit code to decide. A
	// timeout, stop or policy kill outranks it.
	EndReason func() model.EndReason
	// ForceKill, when non-nil, immediately SIGKILLs the running process (and
	// its group, when applicable). Unlike the cmd.Cancel ladder this skips
	// the per-task graceful_stop window — used by the daemon shutdown
	// coordinator to bound total shutdown time.
	ForceKill func()
	// Pid is the process-group leader whose group procstat samples, and Rusage
	// returns the kernel's accounting once Wait has returned. Both are set only
	// by backends whose workload is that process tree (shell); 0/nil otherwise.
	Pid    int
	Rusage func() *syscall.Rusage
}
