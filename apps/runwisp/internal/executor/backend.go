// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"io"

	"github.com/runwisp/runwisp/internal/model"
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
	// ForceKill, when non-nil, immediately SIGKILLs the running process (and
	// its group, when applicable). Unlike the cmd.Cancel ladder this skips
	// the per-task graceful_stop window — used by the daemon shutdown
	// coordinator to bound total shutdown time.
	ForceKill func()
}
