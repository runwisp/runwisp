//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestCaptureTUIScreenshots is the capture half of `bun screenshots` for the
// TUI. It drives the real `runwisp tui` against a demo-seeded daemon in a pty,
// navigates to each docs screen, and dumps the raw (colour-bearing) terminal
// stream to RUNWISP_TUI_SHOOT_DIR as tui-<name>.ansi. The Playwright render half
// (apps/ui/e2e/screenshots/tui.screenshots.ts) replays each stream into xterm.js
// and writes the PNG into apps/docs.
//
// It is skipped unless RUNWISP_TUI_SHOOT_DIR is set, so it never runs in normal
// `bun run ci` (which compiles this package). Run it via the moon `screenshots`
// task, or directly:
//
//	RUNWISP_TUI_SHOOT_DIR=/tmp/frames go test ./tests/e2e -run '^TestCaptureTUIScreenshots$' -count=1
func TestCaptureTUIScreenshots(t *testing.T) {
	outDir := os.Getenv("RUNWISP_TUI_SHOOT_DIR")
	if outDir == "" {
		t.Skip("set RUNWISP_TUI_SHOOT_DIR to regenerate TUI screenshots")
	}

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	dataDir := testutil.ShortTempDir(t)
	configPath := filepath.Join(dataDir, "runwisp.toml")
	seedDemoConfig(t, projectDir, binaryPath, configPath, dataDir)
	daemon := startDaemonOn(t, projectDir, binaryPath, configPath, dataDir, reserveTCPPort(t))

	shots := []tuiShot{
		// Home: the landing screen. Wait for the recent-activity table to
		// populate (column headers + the footer run count) so the capture isn't
		// taken mid-load.
		{name: "home", capture: func(t *testing.T, s *tuiSession) {
			s.waitForAll(t, 10*time.Second, "TASK", "TRIGGER", "executions")
		}},
		// Task detail: a scheduled health check with run history + Run Now.
		{name: "task-detail", capture: func(t *testing.T, s *tuiSession) {
			selectSidebarItem(t, s, "healthcheck-api")
			s.waitForAll(t, 5*time.Second, "healthcheck-api", "Run Now")
		}},
		// Info page: system metrics + configuration summary.
		{name: "info", capture: func(t *testing.T, s *tuiSession) {
			selectSidebarItem(t, s, "Info")
			s.waitForAll(t, 5*time.Second, "Configuration")
		}},
		// Run detail: open the most-recent run of a manual job that emits a big,
		// line-numbered log. reindex-search always succeeds.
		{name: "run-detail", capture: func(t *testing.T, s *tuiSession) {
			openReindexRun(t, s)
		}},
		// Fullscreen log view: run detail with `f` pressed — no sidebar/header.
		{name: "fullscreen-mode", capture: func(t *testing.T, s *tuiSession) {
			openReindexRun(t, s)
			s.press(t, "f")
			s.waitForAll(t, 5*time.Second, "[reindex]")
		}},
		// Quit confirmation: the keep-running / shut-down dialog (do NOT confirm,
		// so the daemon survives for any later shot).
		{name: "quit-confirmation", capture: func(t *testing.T, s *tuiSession) {
			s.press(t, "q")
			s.waitForAll(t, 5*time.Second, "Keep Running", "Shut Down")
		}},
	}

	for _, shot := range shots {
		t.Run(shot.name, func(t *testing.T) {
			s := startRemoteTUIEnv(t, projectDir, binaryPath, configPath, daemon,
				"TERM=xterm-256color", "COLORTERM=truecolor")
			shot.capture(t, s)
			// Let the final frame settle (animations, late SSE rows) before
			// grabbing the cumulative stream.
			time.Sleep(400 * time.Millisecond)

			path := filepath.Join(outDir, "tui-"+shot.name+".ansi")
			require.NoError(t, os.WriteFile(path, s.output.Bytes(), 0o600))
			require.NotEmpty(t, s.output.Bytes(), "captured stream should not be empty")

			// SIGTERM only — never confirm "Shut Down", which would kill the
			// shared daemon the remaining shots need.
			s.forceStop()
		})
	}
}

type tuiShot struct {
	name    string
	capture func(t *testing.T, s *tuiSession)
}

// seedDemoConfig writes the embedded demo config to configPath and seeds dataDir
// with believable history, then exits — exactly like the Web UI screenshot
// harness (apps/ui/e2e/screenshots/global-setup.ts).
func seedDemoConfig(t *testing.T, projectDir, binaryPath, configPath, dataDir string) {
	t.Helper()

	cmd := exec.Command(binaryPath, "--config", configPath, "--data", dataDir, "demo", "--seed-only")
	cmd.Dir = projectDir
	cmd.Env = subprocEnv("TERM=dumb")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "demo --seed-only:\n%s", string(out))
}

// selectSidebarItem moves the sidebar selection onto the named task or page.
// The cursor highlight is a style (invisible in the vt10x text dump), so we
// detect selection via the literal "▸" indicator the sidebar prints on the
// selected row. Pressing Enter selects the cursor item; Down advances it
// (auto-skipping group headers). Robust to the demo's group/sort order — a
// wrong guess would just never match and fail loudly here.
func selectSidebarItem(t *testing.T, s *tuiSession, name string) {
	t.Helper()

	const maxItems = 40
	for i := 0; i < maxItems; i++ {
		s.press(t, keyEnter)
		if selectedSidebarRowHas(s.snapshot(), name) {
			return
		}
		s.press(t, keyDown)
	}
	require.FailNowf(t, "could not select sidebar item", "name=%q\nscreen:\n%s", name, s.snapshot())
}

func selectedSidebarRowHas(screen, name string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if strings.ContainsRune(line, '▸') && strings.Contains(line, name) {
			return true
		}
	}
	return false
}

// openReindexRun selects the reindex-search task, focuses the run list, and
// opens its most-recent run (line-numbered log + SUCCESS header).
func openReindexRun(t *testing.T, s *tuiSession) {
	t.Helper()

	selectSidebarItem(t, s, "reindex-search")
	s.waitForAll(t, 5*time.Second, "reindex-search", "Run Now")
	// Right focuses the run list; Enter opens the row-0 (newest) run.
	s.press(t, keyRight, keyEnter)
	s.waitForAll(t, 10*time.Second, "← Back", "[reindex]")
}
