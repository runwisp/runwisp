// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// containerConnectTimeout bounds the first-connect probe (NewContainerBackend
// pings the daemon). A hung engine — socket accepts but never answers — must
// not block the probe indefinitely, because it runs under l.mu and would wedge
// every other task's container start behind it. A var (not const) so tests can
// shrink it. Kin to containerCleanupTimeout, which guards the same shared-lock
// concern on the teardown side.
var containerConnectTimeout = 10 * time.Second

// LazyContainerBackend defers Docker daemon connection until the first
// execution attempt. This prevents startup failure when the Docker daemon
// is temporarily unavailable.
type LazyContainerBackend struct {
	mu      sync.Mutex
	backend *ContainerBackend
}

// NewLazyContainerBackend returns a Backend that connects to Docker on first use.
func NewLazyContainerBackend() Backend {
	return &LazyContainerBackend{}
}

func (l *LazyContainerBackend) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error) {
	b, err := l.ensureConnected(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker backend unavailable: %w", err)
	}
	return b.Start(ctx, task, run, def)
}

func (l *LazyContainerBackend) Available(ctx context.Context) bool {
	b, err := l.ensureConnected(ctx)
	if err != nil {
		return false
	}
	return b.Available(ctx)
}

func (l *LazyContainerBackend) ensureConnected(ctx context.Context) (*ContainerBackend, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.backend != nil {
		return l.backend, nil
	}
	// Bound the probe: NewContainerBackend pings the daemon, and the run's ctx
	// carries no deadline of its own (a service run outlives any timeout), so a
	// hung engine would block here — and hold l.mu — forever, wedging every other
	// container task's start behind it. The caller's ctx still cancels earlier if
	// the run is stopped.
	probeCtx, cancel := context.WithTimeout(ctx, containerConnectTimeout)
	defer cancel()
	b, err := NewContainerBackend(probeCtx)
	if err != nil {
		return nil, err
	}
	l.backend = b
	return b, nil
}
