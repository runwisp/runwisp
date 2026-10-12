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

	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
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
	t.Parallel()
	castFile := os.Getenv("RUNWISP_TUI_CAST_FILE")
	if castFile == "" {
		t.Skip("set RUNWISP_TUI_CAST_FILE to record the animated TUI demo")
	}

	projectDir := runwispProjectDir(t)
	// The moon task passes its version-stamped `~:build` binary so the TUI
	// shows the release version; the e2e fallback build reports 0.0.0-dev.
	binaryPath := os.Getenv("RUNWISP_TUI_SHOOT_BINARY")
	if binaryPath == "" {
		binaryPath = buildRunwispBinary(t, projectDir)
	}
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

	m := &demoMouse{s: s, col: 90, row: 12}

	// Home: the pointer sweeps the recent-activity table (rows highlight under
	// it), then the wheel scrolls it.
	m.glideTo(t, 70, 8)
	m.glideTo(t, 70, 20)
	m.wheel(t, true, 4)
	m.wheel(t, false, 4)
	time.Sleep(300 * time.Millisecond)

	// Failures stay visible: cycle the status filter to FAILED
	// (all -> running -> succeeded -> failed), then double-click the top failed
	// run to show its captured stderr.
	s.press(t, keyRight)
	time.Sleep(300 * time.Millisecond)
	s.press(t, "f", "f", "f")
	s.waitForAll(t, 5*time.Second, "FILTER: FAILED")
	time.Sleep(700 * time.Millisecond)
	m.doubleClick(t, "failed", mainPanel)
	s.waitForAll(t, 10*time.Second, "← Back")
	time.Sleep(1500 * time.Millisecond)
	m.click(t, "← Back", mainPanel)
	time.Sleep(300 * time.Millisecond)
	// The status filter sticks across tasks; cycle it back to "all"
	// (failed -> skipped -> stopped -> all) so later task views show their runs.
	s.pressEvery(t, 150*time.Millisecond, "f", "f", "f")
	time.Sleep(300 * time.Millisecond)

	// Live in-place redraws: rotate-logs redraws four per-file progress bars
	// with cursor-up escapes. Click it in the sidebar, click Run Now, and watch
	// the exec view stream them.
	m.click(t, "rotate-logs", sidebar)
	s.waitForAll(t, 5*time.Second, "Run Now")
	m.click(t, "Run Now", mainPanel)
	confirmRun(t, s, "rotate-logs")
	s.waitForAll(t, 15*time.Second, "done")
	time.Sleep(1500 * time.Millisecond)

	// Log search over a huge log: reindex-search wrote 8000 lines. Search, jump
	// to a hit, then wheel through the surrounding lines.
	m.click(t, "← Back", mainPanel)
	time.Sleep(300 * time.Millisecond)
	s.press(t, keyLeft)
	jumpToTask(t, s, "rei", "reindex-search")
	// Right focuses the run list; "/" there searches every run of the task. The
	// query matches one line near the end of each run's log: a hit outside the
	// loaded tail page would open the run without scrolling to it.
	s.press(t, keyRight)
	time.Sleep(500 * time.Millisecond)
	s.press(t, "/")
	time.Sleep(400 * time.Millisecond)
	s.press(t, strings.Split("org=7 id=0000796", "")...)
	time.Sleep(300 * time.Millisecond)
	s.press(t, keyEnter)
	s.waitForAll(t, 30*time.Second, "L7963")
	time.Sleep(1200 * time.Millisecond)
	s.press(t, keyEnter)
	s.waitForAll(t, 10*time.Second, "← Back", "[reindex]")
	time.Sleep(1200 * time.Millisecond)
	m.wheel(t, true, 6)
	m.wheel(t, false, 10)
	time.Sleep(400 * time.Millisecond)

	// Finale: a scheduled backup whose run streams coloured SQL and live `\r`
	// progress bars.
	m.click(t, "← Back", mainPanel)
	time.Sleep(300 * time.Millisecond)
	m.click(t, "backup-postgres", sidebar)
	s.waitForAll(t, 5*time.Second, "Run Now")
	m.click(t, "Run Now", mainPanel)
	confirmRun(t, s, "backup-postgres")
	s.waitForAll(t, 8*time.Second, "uploaded to s3")
	time.Sleep(1200 * time.Millisecond)

	entries := s.castSnapshot()
	require.NotEmpty(t, entries, "captured cast should not be empty")
	writeCast(t, castFile, markTime, entries, s.keySnapshot())

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
	Keys     []tuiKeyEntry  `json:"keys"`
}

// writeCast serializes a cast to path as JSON.
func writeCast(t *testing.T, path string, markTime time.Duration, entries []tuiCastEntry, keys []tuiKeyEntry) {
	t.Helper()

	data, err := json.Marshal(tuiCastFile{MarkTime: markTime.Seconds(), Entries: entries, Keys: keys})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

// jumpToTask opens the sidebar filter, types query one key at a time, and
// selects the task it narrows to. The task name is asserted on screen, so a
// renamed demo task fails loudly instead of recording the wrong tour.
func jumpToTask(t *testing.T, s *tuiSession, query, task string) {
	t.Helper()

	s.press(t, "/")
	time.Sleep(300 * time.Millisecond)
	s.press(t, strings.Split(query, "")...)
	time.Sleep(300 * time.Millisecond)
	s.press(t, keyEnter)
	s.waitForAll(t, 5*time.Second, task, "Run Now")
}

// confirmRun answers the "Run now?" dialog with keys (dialogs switch mouse
// mode off). It defaults to "No", so it switches to "Yes" visibly first (see
// internal/tui/confirm.go).
func confirmRun(t *testing.T, s *tuiSession, task string) {
	t.Helper()

	s.waitForAll(t, 5*time.Second, fmt.Sprintf("Run '%s' now?", task))
	time.Sleep(400 * time.Millisecond)
	s.press(t, keyRight)
	time.Sleep(400 * time.Millisecond)
	s.press(t, keyEnter)
}

// Column ranges (0-based, half-open) for demoMouse.findCell: the sidebar is
// uikit.SidebarWidth wide, the main panel is the rest.
var (
	sidebar   = [2]int{0, 28}
	mainPanel = [2]int{28, 1 << 20}
)

// demoMouse drives the TUI's mouse support (tea.MouseModeAllMotion) with SGR
// sequences written to the PTY. Targets are located on the live screen, so a
// moved button fails loudly. Every event goes through tuiSession.pressEvery and
// is therefore recorded for the replay's pointer overlay.
type demoMouse struct {
	s        *tuiSession
	col, row int // 0-based pointer position
}

func sgrMouse(code, col, row int, release bool) string {
	end := 'M'
	if release {
		end = 'm'
	}
	return fmt.Sprintf("\x1b[<%d;%d;%d%c", code, col+1, row+1, end)
}

// glideTo moves the pointer in short motion steps so hover highlights follow it.
func (m *demoMouse) glideTo(t *testing.T, col, row int) {
	t.Helper()

	const steps = 8
	seqs := make([]string, 0, steps)
	for i := 1; i <= steps; i++ {
		c := m.col + (col-m.col)*i/steps
		r := m.row + (row-m.row)*i/steps
		seqs = append(seqs, sgrMouse(35, c, r, false))
	}
	m.s.pressEvery(t, 35*time.Millisecond, seqs...)
	m.col, m.row = col, row
}

// findCell returns the first cell of text on screen within the column range.
func (m *demoMouse) findCell(t *testing.T, text string, cols [2]int) (col, row int) {
	t.Helper()

	for r, line := range strings.Split(m.s.snapshot(), "\n") {
		runes := []rune(line)
		for c := cols[0]; c < len(runes) && c < cols[1]; c++ {
			if strings.HasPrefix(string(runes[c:]), text) {
				return c, r
			}
		}
	}
	require.FailNowf(t, "mouse target not on screen", "text=%q\nscreen:\n%s", text, m.s.snapshot())
	return 0, 0
}

func (m *demoMouse) click(t *testing.T, text string, cols [2]int) {
	t.Helper()

	col, row := m.findCell(t, text, cols)
	m.glideTo(t, col+1, row)
	time.Sleep(150 * time.Millisecond)
	m.s.pressEvery(t, 80*time.Millisecond, sgrMouse(0, m.col, m.row, false), sgrMouse(0, m.col, m.row, true))
}

// doubleClick clicks twice inside the TUI's doubleClickThreshold (400ms).
func (m *demoMouse) doubleClick(t *testing.T, text string, cols [2]int) {
	t.Helper()

	col, row := m.findCell(t, text, cols)
	m.glideTo(t, col+1, row)
	time.Sleep(150 * time.Millisecond)
	down, up := sgrMouse(0, m.col, m.row, false), sgrMouse(0, m.col, m.row, true)
	m.s.pressEvery(t, 50*time.Millisecond, down, up, down, up)
}

// wheel scrolls n notches at the current pointer position.
func (m *demoMouse) wheel(t *testing.T, down bool, n int) {
	t.Helper()

	code := 64
	if down {
		code = 65
	}
	seqs := make([]string, n)
	for i := range seqs {
		seqs[i] = sgrMouse(code, m.col, m.row, false)
	}
	m.s.pressEvery(t, 120*time.Millisecond, seqs...)
}
