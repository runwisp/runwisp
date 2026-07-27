// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"net"
	"testing"

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
