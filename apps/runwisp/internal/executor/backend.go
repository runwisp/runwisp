// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"context"
	"io"

	"github.com/runwisp/runwisp/internal/model"
)

// Backend executes a specific task execution type.
// Implementations must write all output to the provided stdout/stderr writers
// and return the process exit code (0 = success).
type Backend interface {
	Start(ctx context.Context, def model.ExecutionDef) (*Process, error)
	Available(ctx context.Context) bool
}

// Process represents a running execution whose output can be streamed.
type Process struct {
	Stdout  io.ReadCloser // main output stream
	Stderr  io.ReadCloser // error stream (nil when not applicable)
	Wait    func() (exitCode int, err error)
	Cleanup func() // optional: clean up temp resources after Wait
}
