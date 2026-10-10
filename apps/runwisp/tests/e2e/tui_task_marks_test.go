//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTUISidebarFailedMarkClearsWhenTheRunIsDeleted drives the sidebar mark end
// to end: a failed run puts a ✗ on its task, and deleting that run drops the
// mark once the task-state poll (/api/tasks lastRun) reports no last run. Before
// the marks followed the server, the TUI kept the failure it had seen in a
// loaded run list and showed the ✗ until restart.
func TestTUISidebarFailedMarkClearsWhenTheRunIsDeleted(t *testing.T) {
	t.Parallel()
	suite := newTUISuite(t)
	client := suite.client(t)

	_, err := client.TriggerRun(t.Context(), "bravo-fail", nil, "")
	require.NoError(t, err)
	run := waitForEndedRun(t, client, "bravo-fail")

	suite.tui.waitForScreen(t, 10*time.Second, "a ✗ mark on the failed task",
		func(screen string) bool { return strings.Contains(screen, "✗") })

	require.NoError(t, client.DeleteRun(t.Context(), run.ID))

	// The poll runs every few seconds; allow a couple of rounds.
	suite.tui.waitForScreen(t, 15*time.Second, "the ✗ mark gone with its run",
		func(screen string) bool { return !strings.Contains(screen, "✗") })

	suite.quit(t)
}
