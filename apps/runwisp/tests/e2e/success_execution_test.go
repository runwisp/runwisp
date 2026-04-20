//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTUIStreamsLiveLogsAndSettlesSuccessfulHeaderState(t *testing.T) {
	suite := newTUISuite(t)
	suite.selectAlphaTask(t)

	suite.tui.press(t, "r")
	suite.tui.waitForAll(t, 5*time.Second, "Run Now", "Trigger a new run of")
	suite.tui.press(t, "y")

	runningScreen := suite.tui.waitForAll(t, 5*time.Second,
		"alpha-stream",
		"running",
		"alpha-line-1",
		"■ Stop (s)",
	)
	require.NotContains(t, runningScreen, "alpha-line-3")

	completedScreen := suite.tui.waitForAll(t, 8*time.Second,
		"success",
		"alpha-line-3",
	)
	require.NotContains(t, completedScreen, "■ Stop (s)")
	require.NotContains(t, completedScreen, "↻ Retry (r)")

	suite.tui.quitAndShutdown(t)
}
