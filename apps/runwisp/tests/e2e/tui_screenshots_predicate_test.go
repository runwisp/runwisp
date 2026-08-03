//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import "testing"

// These run unconditionally — unlike TestCaptureTUIScreenshots, which is gated on
// RUNWISP_TUI_SHOOT_DIR and therefore never runs in `bun run ci`. That gate is why
// the home shot's readiness check could sit broken from the day it was written: it
// waited for the literal "executions", which appears only in the *empty*-table
// message ("No executions yet…"), so against a demo-seeded daemon it could never
// match and the shot always timed out. Keeping the predicate pure and testing it
// here means the sentinel can't rot unnoticed again.

// homeScreenPopulated is a real capture of the demo-seeded home screen, trimmed to
// the rows that matter. Note the sidebar on the left and the table on the right.
const homeScreenPopulated = `
 ⟡ RunWisp                    Home
   v0.0.0-dev
   still-dew
                              ⮕  Open Web UI
 ▸ Home                       Web UI  https://runwisp.acme-notes.example
 Backups                      Password  ••••••••••••••••••••••
     backup-postgres
     backup-restore-test      TASK                        STATUS       STARTED     DURATION   TRIGGER
 Health                       queue-worker#2              running      9s ago      9.1s       service
     cache-prewarm-probe      queue-worker#1              running      9s ago      9.1s       service
     healthcheck-api          healthcheck-api             success      1m ago      125ms      cron
`

// homeScreenEmpty is the same screen before any run rows have landed: headers
// painted, body still showing the empty-state message.
const homeScreenEmpty = `
 ⟡ RunWisp                    Home
   v0.0.0-dev
 ▸ Home                       Web UI  https://runwisp.acme-notes.example
 Backups                      Password  ••••••••••••••••••••••
     backup-postgres          TASK                        STATUS       STARTED     DURATION   TRIGGER
     backup-restore-test      No executions yet. Waiting for tasks to run...
`

// homeScreenLoading is the screen before the table itself has rendered.
const homeScreenLoading = `
 ⟡ RunWisp                    Home
   v0.0.0-dev
 ▸ Home                       Web UI  https://runwisp.acme-notes.example
 Backups                      Password  ••••••••••••••••••••••
`

func TestRecentActivityPopulated(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		want   bool
	}{
		{"populated table", homeScreenPopulated, true},
		{"header but no rows", homeScreenEmpty, false},
		{"table not yet rendered", homeScreenLoading, false},
		{"blank screen", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := recentActivityPopulated(tc.screen); got != tc.want {
				t.Errorf("recentActivityPopulated() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A row above the header must not count — the chrome above the table (the Web UI
// URL, the fingerprint) is outside the region the predicate inspects.
func TestRecentActivityPopulatedIgnoresRowsAboveHeader(t *testing.T) {
	screen := `
 ▸ Home                       Last synced 4m ago
 Backups                      TASK          STATUS    STARTED    DURATION   TRIGGER
     backup-postgres          No executions yet. Waiting for tasks to run...
`
	if recentActivityPopulated(screen) {
		t.Error("recentActivityPopulated() = true, want false: ' ago' above the header is not a run row")
	}
}
