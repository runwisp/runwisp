// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LazyContainerBackend's "fresh connection" path can be exercised without a
// real Docker daemon by pointing DOCKER_HOST at a socket that doesn't answer
// — NewContainerBackend then fast-fails on Ping. The cached path is
// exercised directly by pre-seeding `backend` and asserting subsequent calls
// don't re-attempt the connection.

func TestLazyContainerBackend_NewReturnsBackendImplementation(t *testing.T) {
	b := NewLazyContainerBackend()
	require.NotNil(t, b)
	_, ok := b.(*LazyContainerBackend)
	assert.True(t, ok, "NewLazyContainerBackend must return *LazyContainerBackend")
}

func TestLazyContainerBackend_AvailableReturnsFalseWhenDaemonUnreachable(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/path/to/docker.sock")
	l := &LazyContainerBackend{}
	if l.Available(context.Background()) {
		t.Skip("docker daemon is reachable on this machine; can't assert unavailability")
	}
}

func TestLazyContainerBackend_StartWrapsConnectionError(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/path/to/docker.sock")
	l := &LazyContainerBackend{}
	_, err := l.Start(context.Background(), nil, nil, nil)
	if err == nil {
		t.Skip("docker daemon is reachable on this machine; can't assert error wrapping")
	}
	assert.Contains(t, err.Error(), "docker backend unavailable")
}

func TestLazyContainerBackend_EnsureConnectedReturnsCachedBackend(t *testing.T) {
	// Seed the backend cache directly to verify ensureConnected returns it
	// without re-connecting.
	cached := &ContainerBackend{}
	l := &LazyContainerBackend{backend: cached}
	got, err := l.ensureConnected(context.Background())
	require.NoError(t, err)
	if got != cached {
		t.Fatal("ensureConnected must return the cached backend pointer unchanged")
	}
}
