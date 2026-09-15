//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestCaptureTUIDemo is the capture half of `bun demo-tui`, the README's
// animated TUI hero. Unlike TestCaptureTUIScreenshots (a fresh session per
// screen, dumping one cumulative ANSI end-state each), this drives ONE
// continuous session through a short tour and records the raw PTY stream with
// per-chunk timestamps, so the Playwright replay
// (apps/ui/e2e/screenshots/tui.demo.ts) can reproduce the original timing:
// the log streaming in, screen transitions, while a DevTools screencast
// captures frames.
//
// Skipped unless RUNWISP_TUI_CAST_FILE is set, so it never runs in
// `bun run ci`. Run it via the moon `demo-tui` task, or directly:
//
//	RUNWISP_TUI_CAST_FILE=/tmp/cast.json go test ./tests/e2e -run '^TestCaptureTUIDemo$' -count=1
func TestCaptureTUIDemo(t *testing.T) {
	castFile := os.Getenv("RUNWISP_TUI_CAST_FILE")
	if castFile == "" {
		t.Skip("set RUNWISP_TUI_CAST_FILE to record the animated TUI demo")
	}

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	dataDir := testutil.ShortTempDir(t)
	configPath := filepath.Join(dataDir, "runwisp.toml")
	seedDemoConfig(t, projectDir, binaryPath, configPath, dataDir)
	daemon := startDaemonOn(t, projectDir, binaryPath, configPath, dataDir, reserveTCPPort(t))

	s := startRemoteTUIEnv(t, projectDir, binaryPath, configPath, daemon,
		"TERM=xterm-256color", "COLORTERM=truecolor")
	defer s.forceStop()

	// Home: wait for the recent-activity table to populate, then mark this as
	// the point the replay should switch from an instant fast-forward to real
	// time. Everything before it (daemon connect, initial paint) still gets
	// replayed, into a fresh browser terminal that needs it to reach the same
	// state, just without spending clip time on it, so the clip opens on the
	// clean, populated Home screen with no "Connecting…" flash.
	s.waitForScreen(t, 10*time.Second, "populated recent-activity table", recentActivityPopulated)
	time.Sleep(300 * time.Millisecond)
	markTime := s.elapsed()
	time.Sleep(800 * time.Millisecond)

	// Scroll the homepage's recent-activity table down to a run with a long,
	// scrollable log (reindex-search: one line per indexed document, thousands
	// of lines), and open it.
	s.press(t, keyRight)
	selectHomeActivityRow(t, s, "reindex-search")
	time.Sleep(500 * time.Millisecond)
	s.press(t, keyEnter)
	s.waitForAll(t, 10*time.Second, "← Back", "[reindex]")
	time.Sleep(800 * time.Millisecond)

	// Scroll its log in place (not fullscreen). Opening a run always lands
	// header focus on "← Back" (see openExecView in model.go), and Up/Down
	// there just walk the header fields, not the log, so two silent Downs
	// move focus off the header and into the pane first. From there, a
	// slow, one-line-at-a-time scroll back through history: each press
	// only moves the view by a single line, so consecutive frames overlap
	// and the motion reads as scrolling, unlike a couple of large page
	// jumps between unrelated chunks of the log.
	s.press(t, keyDown, keyDown)
	s.press(t, repeatKey(keyUp, 30)...)
	time.Sleep(600 * time.Millisecond)

	// Back to the homepage list, then to the sidebar.
	s.press(t, keyEsc)
	time.Sleep(400 * time.Millisecond)
	s.press(t, keyLeft)
	time.Sleep(300 * time.Millisecond)

	// Sidebar: a scheduled backup whose run streams a live `\r` progress bar,
	// showcasing that RunWisp captures in-place terminal redraws, not just
	// line-by-line logs.
	const backupTask = "backup-postgres"
	selectSidebarItem(t, s, backupTask)
	s.waitForAll(t, 5*time.Second, backupTask, "Run Now")
	s.press(t, keyRight)
	time.Sleep(500 * time.Millisecond)

	// Run it now: trigger, confirm, and watch the exec view that opens
	// automatically stream the progress bars to completion.
	s.press(t, "r")
	s.waitForAll(t, 5*time.Second, fmt.Sprintf("Run '%s' now?", backupTask))
	time.Sleep(500 * time.Millisecond)
	// The dialog defaults to "No" selected (see internal/tui/confirm.go), so
	// switch to "Yes" first, visibly, before confirming with Enter.
	s.press(t, keyRight)
	time.Sleep(500 * time.Millisecond)
	s.press(t, keyEnter)
	s.waitForAll(t, 8*time.Second, "uploaded to s3")
	time.Sleep(900 * time.Millisecond)

	entries := s.castSnapshot()
	require.NotEmpty(t, entries, "captured cast should not be empty")
	writeCast(t, castFile, markTime, entries)

	// SIGTERM only, never confirm "Shut Down".
	s.forceStop()
}

// tuiCastFile is the on-disk cast format the Playwright replay
// (apps/ui/e2e/screenshots/tui.demo.ts) consumes: MarkTime is the elapsed-time
// boundary between the instant fast-forward prefix and the real-time portion
// of Entries.
type tuiCastFile struct {
	MarkTime float64        `json:"markTime"`
	Entries  []tuiCastEntry `json:"entries"`
}

// writeCast serializes a cast to path as JSON.
func writeCast(t *testing.T, path string, markTime time.Duration, entries []tuiCastEntry) {
	t.Helper()

	data, err := json.Marshal(tuiCastFile{MarkTime: markTime.Seconds(), Entries: entries})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// repeatKey returns key repeated n times, for driving a slow, one-line-at-a-
// time scroll (see the reindex-search log step) instead of a couple of large
// jumps that read as the view hopping between unrelated pages.
func repeatKey(key string, n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = key
	}
	return keys
}

// selectHomeActivityRow scrolls the homepage's (already-focused) recent-
// activity table downward until the cursor lands on a row for name, so a
// following Enter opens that run. The demo seeds a handful of manual runs for
// name within the last several minutes (see internal/demo/seed.go's
// planManual), so it always lands within the first couple of screens; maxRows
// is a generous bound in case other tasks' runs crowd it further down.
func selectHomeActivityRow(t *testing.T, s *tuiSession, name string) {
	t.Helper()

	const maxRows = 60
	for i := 0; i < maxRows; i++ {
		if homeActivityCursorRowHas(s.snapshot(), i, name) {
			return
		}
		s.press(t, keyDown)
	}
	require.FailNowf(t, "could not scroll home activity to row", "name=%q\nscreen:\n%s", name, s.snapshot())
}

// homeActivityCursorRowHas reports whether name appears in the row currently
// under the activity table's cursor. The cursor highlight is a colour
// (invisible in vt10x's plain-text snapshot, like the sidebar's, see
// selectSidebarItem), but its position is otherwise fully determined:
// cursorIndex tracks the number of Down presses since the table gained focus
// (its cursor always starts at row 0), and the footer's "viewing A-B of N"
// line reports the current scroll offset, so the row at cursorIndex-offset in
// the data section is always the one the cursor sits on.
func homeActivityCursorRowHas(screen string, cursorIndex int, name string) bool {
	lines := strings.Split(screen, "\n")

	headerIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "TASK") && strings.Contains(line, "TRIGGER") {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 {
		return false
	}
	dataLines := lines[headerIdx+1:]

	scrollOffset := 0
	for _, line := range dataLines {
		var from, to, total int
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "viewing %d–%d of %d", &from, &to, &total); err == nil {
			scrollOffset = from - 1
			break
		}
	}

	rowInView := cursorIndex - scrollOffset
	if rowInView < 0 || rowInView >= len(dataLines) {
		return false
	}
	return strings.Contains(dataLines[rowInView], name)
}
