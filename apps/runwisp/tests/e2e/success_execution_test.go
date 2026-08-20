//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTUIStreamsLiveLogsAndSettlesSuccessfulHeaderState(t *testing.T) {
	suite := newTUISuite(t)
	suite.selectAlphaTask(t)

	// Run Now opens a confirm dialog; press y to confirm.
	suite.tui.press(t, "r")
	suite.tui.waitForAll(t, 3*time.Second, "Run 'alpha-stream' now?")
	suite.tui.press(t, "y")

	runningScreen := suite.tui.waitForAll(t, 5*time.Second,
		"alpha-stream",
		"running",
		"alpha-line-1",
		"■ Stop (s)",
	)
	require.NotContains(t, runningScreen, "alpha-line-3")

	completedScreen := suite.tui.waitForAll(t, 8*time.Second,
		"succeeded",
		"alpha-line-3",
	)
	require.NotContains(t, completedScreen, "■ Stop (s)")
	require.NotContains(t, completedScreen, "↻ Retry (r)")

	suite.tui.quitAndShutdown(t)
}
