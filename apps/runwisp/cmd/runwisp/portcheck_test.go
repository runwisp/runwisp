// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"net"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortConflictError(t *testing.T) {
	bind := errors.New("bind: address already in use")

	t.Run("explicit host+port appears in message; user-facing wrapper exposed", func(t *testing.T) {
		err := portConflictError("192.168.1.10", 8765, bind)
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, "192.168.1.10")
		assert.Contains(t, msg, "8765")

		ufe, ok := isUserFacing(err)
		require.True(t, ok, "portConflictError must return a *userFacingError")
		assert.Contains(t, ufe.details, "8765")
	})

	t.Run("empty host falls back to loopback", func(t *testing.T) {
		err := portConflictError("", 9000, bind)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "127.0.0.1")
	})
}

func TestProbePortAvailable_FreePort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	require.NoError(t, probePortAvailable("127.0.0.1", port))
}

func TestProbePortAvailable_BusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	require.Error(t, probePortAvailable("127.0.0.1", port))
}

func TestProbePortAvailable_EmptyHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	require.NoError(t, probePortAvailable("", port))
}

func TestFreePortIsBindable(t *testing.T) {
	port, err := freePort("127.0.0.1")
	if err != nil || port <= 0 {
		t.Fatalf("freePort = %d, %v", port, err)
	}
	if err := probePortAvailable("127.0.0.1", port); err != nil {
		t.Fatalf("port %d not bindable: %v", port, err)
	}
}

func TestDemoResolvePort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	taken := ln.Addr().(*net.TCPAddr).Port

	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{}
		cmd.Flags().Int("port", 0, "")
		return cmd
	}

	t.Run("free port is kept", func(t *testing.T) {
		f := Flags{Host: "127.0.0.1"}
		free, err := freePort(f.Host)
		require.NoError(t, err)
		f.Port = free
		require.NoError(t, demoResolvePort(newCmd(), &f))
		require.Equal(t, free, f.Port)
	})
	t.Run("default port taken moves to a free one", func(t *testing.T) {
		f := Flags{Host: "127.0.0.1", Port: taken}
		require.NoError(t, demoResolvePort(newCmd(), &f))
		require.NotEqual(t, taken, f.Port)
		require.NoError(t, probePortAvailable(f.Host, f.Port))
	})
	t.Run("explicit port taken is an error", func(t *testing.T) {
		cmd := newCmd()
		require.NoError(t, cmd.Flags().Set("port", strconv.Itoa(taken)))
		f := Flags{Host: "127.0.0.1", Port: taken}
		require.Error(t, demoResolvePort(cmd, &f))
		require.Equal(t, taken, f.Port)
	})
}
