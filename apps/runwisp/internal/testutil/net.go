// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// PickFreePort grabs an ephemeral TCP port on the loopback interface and
// immediately releases it so the caller can bind to it. The window between
// release and re-bind is inherently racy but acceptable for serial tests.
func PickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}
