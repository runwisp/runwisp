// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

// loadDemoConfig writes the embedded TOML to a temp file and loads it through
// the real config loader, exactly as the demo command does.
func loadDemoConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")
	if err := WriteConfig(path); err != nil {
		t.Fatalf("write demo config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load demo config: %v", err)
	}
	return cfg
}

func TestSeed(t *testing.T) {
	cfg := loadDemoConfig(t)
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	logDir := filepath.Join(t.TempDir(), "logs")
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	n, err := Seed(ctx, db, cfg, logDir, now)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if n < minRuns {
		t.Fatalf("seeded %d runs, want at least %d", n, minRuns)
	}

	// Every planned run must be persisted.
	sum, err := db.GetRunSummary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.Total != int64(n) {
		t.Fatalf("summary total = %d, want %d", sum.Total, n)
	}

	t.Run("scheduled runs align to cron and have finalized logs", func(t *testing.T) {
		// backup-postgres is "15 3 * * *": every fire lands on minute 15
		// (Bratislava is a whole-hour offset, so the minute is tz-invariant).
		runs := queryAll(t, db, "backup-postgres")
		if len(runs) == 0 {
			t.Fatal("no backup-postgres runs")
		}
		for _, r := range runs {
			if r.StartAt == nil || r.StartAt.Minute() != 15 {
				t.Fatalf("run %s start minute = %v, want 15", r.ID, r.StartAt)
			}
			if r.TriggeredBy != model.TriggeredByCron {
				t.Errorf("run %s triggered_by = %q, want cron", r.ID, r.TriggeredBy)
			}
			assertFinalizedLog(t, logDir, r, 1)
		}
	})

	t.Run("manual heavy task produces a very long finalized log", func(t *testing.T) {
		runs := queryAll(t, db, "reindex-search")
		if len(runs) == 0 {
			t.Fatal("no reindex-search runs")
		}
		var maxLines int64
		for _, r := range runs {
			if r.TriggeredBy != model.TriggeredByAPI {
				t.Errorf("manual run %s triggered_by = %q, want api", r.ID, r.TriggeredBy)
			}
			if lines := assertFinalizedLog(t, logDir, r, 1); lines > maxLines {
				maxLines = lines
			}
		}
		if maxLines < 5000 {
			t.Fatalf("longest reindex-search log = %d lines, want a long (>5000) one", maxLines)
		}
	})

	t.Run("service runs carry instance index and service trigger", func(t *testing.T) {
		runs := queryAll(t, db, "queue-worker")
		if len(runs) == 0 {
			t.Fatal("no queue-worker runs")
		}
		sawInstance1 := false
		for _, r := range runs {
			if r.TriggeredBy != model.TriggeredByService {
				t.Errorf("service run %s triggered_by = %q, want service", r.ID, r.TriggeredBy)
			}
			if r.InstanceIndex == 1 {
				sawInstance1 = true
			}
		}
		if !sawInstance1 {
			t.Error("expected at least one queue-worker run on instance index 1")
		}
	})

	t.Run("all runs are terminal and backdated", func(t *testing.T) {
		runs := queryAll(t, db, "")
		for _, r := range runs {
			if r.Status != model.PhaseEnded {
				t.Fatalf("run %s status = %q, want ended", r.ID, r.Status)
			}
			if r.StartAt == nil || !r.StartAt.Before(now) {
				t.Fatalf("run %s start %v not before now %v", r.ID, r.StartAt, now)
			}
		}
	})
}

// queryAll pages through every run for a task (empty taskName = all tasks).
func queryAll(t *testing.T, db storage.Database, taskName string) []model.Run {
	t.Helper()
	var all []model.Run
	const page = 500
	for offset := 0; ; offset += page {
		runs, err := db.QueryRuns(context.Background(), taskName, page, offset,
			"", storage.SortColumnDefault, storage.SortDirectionDefault, "")
		if err != nil {
			t.Fatalf("query runs: %v", err)
		}
		all = append(all, runs...)
		if len(runs) < page {
			break
		}
	}
	return all
}

// assertFinalizedLog checks the run's log file exists with a finalized .meta and
// returns its total line count.
func assertFinalizedLog(t *testing.T, logDir string, r model.Run, minLines int64) int64 {
	t.Helper()
	logPath := logutil.ResolveRunLogPath(logDir, r.TaskName, r.ID, r.CreatedAt)
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file missing for run %s: %v", r.ID, err)
	}
	meta := logutil.ReadLogMeta(logPath)
	if !meta.Finalized {
		t.Fatalf("log for run %s not finalized", r.ID)
	}
	total := meta.RotatedLines + meta.FinalLines
	if total < minLines {
		t.Fatalf("log for run %s has %d lines, want >= %d", r.ID, total, minLines)
	}
	return total
}
