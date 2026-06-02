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
	return loadConfigTOML(t, string(ConfigTOML))
}

// loadConfigTOML writes the given TOML to a temp file and loads it through the
// real config loader.
func loadConfigTOML(t *testing.T, toml string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")
	if err := os.WriteFile(path, []byte(toml), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// syntheticConfig is a tiny stand-in for the real demo config: one always-green
// cron task, one always-red cron task that allows a retry, one manual task, and
// a two-instance service. It exercises every seeding mechanic (cron alignment,
// real outcomes, retry linkage, instance indexing) without paying the full
// 30-day real-execution cost — services are bounded to a 50ms sample window and
// the lookback is a few hours.
const syntheticConfig = `
[scheduler]
timezone = "UTC"

[defaults]
keep_runs = 60

[tasks.tick]
cron = "15 * * * *"
run = "sleep 0.02; echo tick-ok"

[tasks.flaky]
cron = "15 * * * *"
retry_attempts = 1
retry_delay = "30s"
run = '''
echo "boom" >&2
exit 1
'''

[tasks.deploy-thing]
api_trigger = true
run = "echo deployed"

[services.worker]
instances = 2
run = '''
echo "started $$"
sleep 30
'''
`

func TestSeed(t *testing.T) {
	cfg := loadConfigTOML(t, syntheticConfig)
	db, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	logDir := filepath.Join(t.TempDir(), "logs")
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	n, err := seedWith(ctx, db, cfg, logDir, now, seedOptions{
		lookback:        6 * time.Hour,
		maxPerScheduled: 50,
		serviceWindow:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if n == 0 {
		t.Fatal("seeded 0 runs")
	}

	// Every executed run must be persisted.
	sum, err := db.GetRunSummary(ctx)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.Total != int64(n) {
		t.Fatalf("summary total = %d, want %d", sum.Total, n)
	}

	t.Run("scheduled runs align to cron, are cron-triggered, and log for real", func(t *testing.T) {
		// "15 * * * *" fires hourly on minute 15.
		runs := queryAll(t, db, "tick")
		if len(runs) == 0 {
			t.Fatal("no tick runs")
		}
		for _, r := range runs {
			if r.StartAt == nil || r.StartAt.Minute() != 15 {
				t.Fatalf("run %s start minute = %v, want 15", r.ID, r.StartAt)
			}
			if r.TriggeredBy != model.TriggeredByCron {
				t.Errorf("run %s triggered_by = %q, want cron", r.ID, r.TriggeredBy)
			}
			if r.EndReason == nil || *r.EndReason != model.ReasonSuccess {
				t.Errorf("run %s end reason = %v, want success (real exit 0)", r.ID, r.EndReason)
			}
			assertFinalizedLog(t, logDir, r, 1)
		}
	})

	t.Run("manual task is api-triggered with a finalized log", func(t *testing.T) {
		runs := queryAll(t, db, "deploy-thing")
		if len(runs) == 0 {
			t.Fatal("no deploy-thing runs")
		}
		for _, r := range runs {
			if r.TriggeredBy != model.TriggeredByAPI {
				t.Errorf("manual run %s triggered_by = %q, want api", r.ID, r.TriggeredBy)
			}
			assertFinalizedLog(t, logDir, r, 1)
		}
	})

	t.Run("service runs carry instance index and the service trigger", func(t *testing.T) {
		runs := queryAll(t, db, "worker")
		if len(runs) == 0 {
			t.Fatal("no worker runs")
		}
		sawInstance1 := false
		for _, r := range runs {
			if r.TriggeredBy != model.TriggeredByService {
				t.Errorf("service run %s triggered_by = %q, want service", r.ID, r.TriggeredBy)
			}
			if r.InstanceIndex == 1 {
				sawInstance1 = true
			}
			assertFinalizedLog(t, logDir, r, 1)
		}
		if !sawInstance1 {
			t.Error("expected at least one worker run on instance index 1")
		}
	})

	t.Run("real failures derive a failed reason and spawn linked retries", func(t *testing.T) {
		runs := queryAll(t, db, "flaky")
		if len(runs) == 0 {
			t.Fatal("no flaky runs")
		}
		ids := make(map[string]bool, len(runs))
		for _, r := range runs {
			ids[r.ID] = true
		}
		var primaries, retries int
		for _, r := range runs {
			if r.EndReason == nil || *r.EndReason != model.ReasonFailed {
				t.Errorf("run %s end reason = %v, want failed (real exit 1)", r.ID, r.EndReason)
			}
			if r.RetryOfRunID == nil {
				primaries++
				continue
			}
			retries++
			if r.RetryAttempt != 1 {
				t.Errorf("retry %s attempt = %d, want 1", r.ID, r.RetryAttempt)
			}
			if !ids[*r.RetryOfRunID] {
				t.Errorf("retry %s points at unknown parent %q", r.ID, *r.RetryOfRunID)
			}
		}
		if primaries == 0 {
			t.Fatal("no primary flaky runs")
		}
		if retries == 0 {
			t.Fatal("expected at least one linked retry for an always-failing retrying task")
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
