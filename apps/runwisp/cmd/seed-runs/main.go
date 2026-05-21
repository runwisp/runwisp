// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// seed-runs inserts synthetic ended runs for a given task directly into the
// SQLite database. Throwaway dev script; not shipped.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

func main() {
	dbPath := flag.String("db", "data/runwisp.db", "Path to runwisp.db")
	taskName := flag.String("task", "healthcheck-api", "Task name to seed")
	count := flag.Int("count", 5000, "Number of runs to insert")
	spreadHours := flag.Int("spread-hours", 168, "Spread created_at across this many past hours (default 7d)")
	failRate := flag.Float64("fail-rate", 0.05, "Fraction of runs that should be failures (0.0-1.0)")
	flag.Parse()

	db, err := storage.New(*dbPath, os.Stderr)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Spread firings evenly across the window so the run list renders a
	// realistic timeline rather than a tight cluster.
	now := time.Now()
	windowEnd := now.Add(-5 * time.Minute) // newest synthetic run ~5min ago
	windowStart := windowEnd.Add(-time.Duration(*spreadHours) * time.Hour)
	step := windowEnd.Sub(windowStart) / time.Duration(*count)

	registered, err := db.GetTaskRegistration(*taskName)
	if err == nil && registered != nil {
		// Task already known; nothing to do.
	} else if err := db.EnsureTaskRegistered(*taskName, windowStart); err != nil {
		log.Fatalf("register task: %v", err)
	}

	rng := rand.New(rand.NewPCG(uint64(now.UnixNano()), 0xC0FFEE)) //nolint:gosec // dev-only seed RNG
	entropy := ulid.Monotonic(&mathReader{r: rng}, 0)

	for i := 0; i < *count; i++ {
		createdAt := windowStart.Add(step * time.Duration(i))
		startAt := createdAt.Add(time.Duration(rng.IntN(400)+50) * time.Millisecond)

		// healthcheck-api: curl github.com — usually <500ms, occasional slow ones.
		durationMS := 120 + rng.IntN(380)
		if rng.IntN(100) < 3 {
			durationMS = 2000 + rng.IntN(4000) // slow outlier
		}
		endAt := startAt.Add(time.Duration(durationMS) * time.Millisecond)

		failed := rng.Float64() < *failRate
		reason := model.ReasonSuccess
		exit := 0
		if failed {
			// Mostly curl exit 7 (couldn't connect) or 22 (HTTP error >= 400).
			if rng.IntN(2) == 0 {
				exit = 7
			} else {
				exit = 22
			}
			reason = model.ReasonFailed
		}

		// 95% triggered by cron (matches the */5 schedule), 5% manual.
		trigger := model.TriggeredByCron
		if rng.IntN(100) < 5 {
			trigger = model.TriggeredByAPI
		}

		id := ulid.MustNew(ulid.Timestamp(createdAt), entropy).String()

		run := &model.Run{
			ID:          id,
			TaskName:    *taskName,
			Status:      model.PhaseEnded,
			EndReason:   &reason,
			ExitCode:    exit,
			StartAt:     &startAt,
			EndAt:       &endAt,
			TriggeredBy: trigger,
			CreatedAt:   createdAt,
		}
		if err := db.CreateRun(run); err != nil {
			log.Fatalf("insert run %d: %v", i, err)
		}
		if (i+1)%500 == 0 {
			fmt.Printf("inserted %d / %d\n", i+1, *count)
		}
	}

	fmt.Printf("done: %d runs of %q inserted across %v\n", *count, *taskName, windowEnd.Sub(windowStart))
}

// mathReader adapts math/rand/v2.Rand to io.Reader for ulid.Monotonic.
type mathReader struct{ r *rand.Rand }

func (m *mathReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(m.r.IntN(256))
	}
	return len(p), nil
}
