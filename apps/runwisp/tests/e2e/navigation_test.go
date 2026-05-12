//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteTUINavigatesAcrossPrimaryScreens(t *testing.T) {
	suite := newTUISuite(t)

	homeScreen := suite.tui.currentScreen(t)
	require.Contains(t, homeScreen, "Home")
	require.Contains(t, homeScreen, "Web UI")

	taskScreen := suite.selectAlphaTask(t)
	require.Contains(t, taskScreen, "No executions yet. Waiting for tasks to run...")

	infoScreen := suite.selectInfoScreen(t)
	require.Contains(t, infoScreen, "Info")

	debugScreen := suite.selectDebugScreen(t)
	require.Contains(t, debugScreen, "FOLLOW")

	suite.tui.quitAndShutdown(t)
}
