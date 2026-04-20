// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"context"
	"fmt"
	"sync"

	"github.com/runwisp/runwisp/internal/model"
)

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

func (l *LazyContainerBackend) Start(ctx context.Context, def model.ExecutionDef) (*Process, error) {
	b, err := l.ensureConnected(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker backend unavailable: %w", err)
	}
	return b.Start(ctx, def)
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
	b, err := NewContainerBackend(ctx)
	if err != nil {
		return nil, err
	}
	l.backend = b
	return b, nil
}
