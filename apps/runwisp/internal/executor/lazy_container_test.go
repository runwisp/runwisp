// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestLazyContainerBackend_EnsureConnectedBoundsHungEngine models a wedged
// engine — a socket that accepts connections but never answers — reached under
// a run context that carries no deadline of its own (a service run). Without
// bounding, the connect Ping blocks forever while holding l.mu, wedging every
// other container task's start. ensureConnected must instead fail within the
// connect timeout.
func TestLazyContainerBackend_EnsureConnectedBoundsHungEngine(t *testing.T) {
	// A short dir: the unix socket path must fit the OS sun_path limit (~104
	// bytes on macOS), which t.TempDir()'s deep path blows past.
	dir, err := os.MkdirTemp("/tmp", "rw")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open and never respond: a hung engine, not an
			// unreachable one (which would fail fast on connect).
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	t.Setenv("DOCKER_HOST", "unix://"+sock)
	old := containerConnectTimeout
	containerConnectTimeout = 200 * time.Millisecond
	defer func() { containerConnectTimeout = old }()

	l := &LazyContainerBackend{}
	done := make(chan error, 1)
	// context.Background() has no deadline — only the connect timeout can save us.
	go func() { _, err := l.ensureConnected(context.Background()); done <- err }()

	select {
	case err := <-done:
		require.Error(t, err, "a hung-engine probe must fail, not connect")
	case <-time.After(3 * time.Second):
		t.Fatal("ensureConnected did not return: the connect probe is not bounded")
	}
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
