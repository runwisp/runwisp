//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTUIShowsRetryForFailedRunsAndRetryStartsANewExecution(t *testing.T) {
	suite := newTUISuite(t)
	client := suite.client(t)
	suite.selectBravoTask(t)

	// Run Now opens a confirm dialog; press y to confirm.
	suite.tui.press(t, "r")
	suite.tui.waitForAll(t, 3*time.Second, "Run 'bravo-fail' now?")
	suite.tui.press(t, "y")

	failedScreen := suite.tui.waitForAll(t, 8*time.Second,
		"bravo-fail",
		"failed",
		"bravo-line-1",
		"bravo-line-2",
		"↻ Retry (r)",
	)
	require.NotContains(t, failedScreen, "■ Stop (s)")

	// Retry opens a confirm dialog; press y to confirm.
	suite.tui.press(t, "r")
	suite.tui.waitForAll(t, 3*time.Second, "Retry 'bravo-fail'?")
	suite.tui.press(t, "y")

	retriedScreen := suite.tui.waitForAll(t, 5*time.Second,
		"running",
		"bravo-line-1",
		"■ Stop (s)",
	)
	require.NotContains(t, retriedScreen, "↻ Retry (r)")

	waitForRunCount(t, client, "bravo-fail", 2, 5*time.Second)

	secondFailureScreen := suite.tui.waitForAll(t, 8*time.Second,
		"bravo-fail",
		"failed",
		"bravo-line-2",
		"↻ Retry (r)",
	)
	require.NotContains(t, secondFailureScreen, "■ Stop (s)")

	suite.tui.quitAndShutdown(t)
}
