//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteTUINavigatesAcrossPrimaryScreens(t *testing.T) {
	suite := newTUISuite(t)

	homeScreen := suite.tui.currentScreen(t)
	require.Contains(t, homeScreen, "Home")
	require.Contains(t, homeScreen, "Web UI")
	require.Contains(t, homeScreen, "Open Web UI")
	// The remote TUI fetches the ephemeral password over the socket and shows
	// the masked field; the plaintext value must never be rendered into a frame.
	require.Contains(t, homeScreen, "Password", "remote TUI should display the Password label")
	require.Contains(t, homeScreen, strings.Repeat("•", 22),
		"password value must be masked with 22 bullets")
	creds, err := suite.client(t).GetLocalCredentials()
	require.NoError(t, err)
	require.NotEmpty(t, creds.Password)
	require.NotContains(t, homeScreen, creds.Password,
		"plaintext password must never appear in a rendered TUI frame")

	taskScreen := suite.selectAlphaTask(t)
	require.Contains(t, taskScreen, "No executions yet. Waiting for tasks to run...")

	infoScreen := suite.selectInfoScreen(t)
	require.Contains(t, infoScreen, "Info")

	debugScreen := suite.selectDebugScreen(t)
	require.Contains(t, debugScreen, "FOLLOW")

	suite.tui.quitAndShutdown(t)
}
