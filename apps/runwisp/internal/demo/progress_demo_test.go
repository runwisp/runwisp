// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package demo

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

// sgrEscape matches a colour-only SGR sequence ("\x1b[…m"). The terminal-aware
// renderer deliberately round-trips colour into the committed log, but every
// cursor-movement, erase, and carriage-return sequence must be interpreted away.
// Stripping colour lets the assertions prove that nothing *else* survived.
var sgrEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestDemoProgressTasksCommitCleanFinalFrames runs the demo tasks that exercise
// in-place output — a `\r` progress bar (backup-postgres), a multi-line ANSI
// redraw (rotate-logs), and partial-line output (deploy-migrate) — through the
// real executor and asserts each lands on disk as a clean final frame: no
// carriage returns, no leftover cursor/erase escapes, and intermediate frames
// collapsed rather than stacked. This pins the demo as an end-to-end smoke test
// of terminal-aware capture, not just believable screenshot filler.
func TestDemoProgressTasksCommitCleanFinalFrames(t *testing.T) {
	cfg := loadDemoConfig(t)

	t.Run("backup-postgres \\r bar collapses to a single final line", func(t *testing.T) {
		body := runDemoTask(t, cfg, "backup-postgres")
		clean := assertNoControlResidue(t, body)

		// The dump and upload bars each pass through dozens of percentages but
		// must persist exactly one final line apiece.
		requireCount(t, clean, "pg_dump", 1)
		requireCount(t, clean, "upload s3", 1)
		requireContains(t, clean, "100%")
		requireContains(t, clean, "uploaded to s3://acme-notes-backups/")
		// Colour was round-tripped, proving SGR survives to disk.
		requireContains(t, body, "\x1b[32m")
	})

	t.Run("rotate-logs multi-line redraw settles to one frame", func(t *testing.T) {
		body := runDemoTask(t, cfg, "rotate-logs")
		clean := assertNoControlResidue(t, body)

		// Four per-file bars redraw in place; the committed frame holds each file
		// exactly once, finished — not one line per animation step.
		requireCount(t, clean, "gz done", 4)
		requireContains(t, clean, "compressed 4 files, freed 312 MB")
		// The "queued" placeholders are overwritten in place, never committed.
		requireCount(t, clean, "queued", 0)
	})

	t.Run("deploy-migrate partial lines commit whole", func(t *testing.T) {
		body := runDemoTask(t, cfg, "deploy-migrate")
		clean := assertNoControlResidue(t, body)

		// Each "applying … " tail merges with its later "ok" into one line.
		requireCount(t, clean, ".sql ... ok", 3)
		requireContains(t, clean, "done in 0.8s")
	})
}

// runDemoTask executes one task from the demo config through the real executor
// and returns the full on-disk log body.
func runDemoTask(t *testing.T, cfg *config.Config, name string) string {
	t.Helper()
	task := findTask(cfg, name)
	if task == nil {
		t.Fatalf("demo config has no task %q", name)
	}

	logDir := filepath.Join(t.TempDir(), "logs")
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	run := &model.Run{
		TaskName:  name,
		Status:    model.PhaseRunning,
		StartedAt: &now,
		CreatedAt: now,
	}
	run.ID = newULID(now, rand.New(rand.NewSource(1)))

	exec := executor.New(executor.Options{LogDir: logDir, HasLocalTasks: true})
	res := exec.Execute(context.Background(), task, run)
	if res.Error != nil {
		t.Fatalf("execute %q: %v", name, res.Error)
	}
	if reason := res.EndReason(); reason != model.ReasonSuccess {
		t.Fatalf("task %q ended %q (exit %d), want success", name, reason, res.ExitCode)
	}

	data, err := os.ReadFile(logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt))
	if err != nil {
		t.Fatalf("read log for %q: %v", name, err)
	}
	return string(data)
}

// assertNoControlResidue strips colour SGR (which is meant to survive) and fails
// if any carriage return or other escape byte remains — i.e. the renderer
// interpreted every redraw sequence instead of storing it raw. Returns the
// colour-stripped body for content assertions.
func assertNoControlResidue(t *testing.T, body string) string {
	t.Helper()
	if strings.Contains(body, "\r") {
		t.Fatalf("committed log still contains a carriage return:\n%q", body)
	}
	clean := sgrEscape.ReplaceAllString(body, "")
	if i := strings.IndexByte(clean, 0x1b); i >= 0 {
		t.Fatalf("committed log still contains a non-colour escape at %d:\n%q", i, clean)
	}
	return clean
}

func requireCount(t *testing.T, body, sub string, want int) {
	t.Helper()
	if got := strings.Count(body, sub); got != want {
		t.Fatalf("count of %q = %d, want %d; body:\n%s", sub, got, want, body)
	}
}

func requireContains(t *testing.T, body, sub string) {
	t.Helper()
	if !strings.Contains(body, sub) {
		t.Fatalf("expected body to contain %q; body:\n%s", sub, body)
	}
}
