// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/robfig/cron/v3"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

// minRuns is the floor on how many historical runs Seed produces. The per-task
// caps below comfortably exceed it; the assertion in the demo command (and the
// unit test) guards against a future edit that quietly drops below it.
const minRuns = 500

// lookback bounds how far back historical runs are scattered. Kept at 30 days
// so everything lands inside the demo config's default keep_for window and the
// daemon's boot-time retention never trims a seeded row.
const lookback = 30 * 24 * time.Hour

// maxPerScheduled caps fires per scheduled task so a `*/5` health probe doesn't
// alone write thousands of files; it still leaves a dense, scroll-worthy history.
const maxPerScheduled = 180

// seederDeps is the slice of storage the seeder needs. SQLiteDatabase satisfies
// it; tests can pass an in-memory database.
type seederDeps interface {
	CreateRun(ctx context.Context, run *model.Run) error
	EnsureTaskRegistered(ctx context.Context, taskName string, firstSeen time.Time) error
}

// Seed populates the database and on-disk log directory with believable
// historical runs for every task in cfg. It returns the number of runs created.
//
// The work splits in two: a cheap single-threaded planning pass (compute fire
// times, outcomes, IDs — order-sensitive because retries reference their
// parent's ULID) followed by a parallel materialization pass that generates log
// bodies and writes both the log files and the run rows. Log generation is the
// expensive part (the heavy manual tasks emit tens of thousands of lines), so
// it fans out across the CPUs.
func Seed(ctx context.Context, db seederDeps, cfg *config.Config, logDir string, now time.Time) (int, error) {
	rng := rand.New(rand.NewSource(now.UnixNano()))

	specs := planRuns(cfg, now, rng)

	// Backdate each task's "first seen" to its oldest run so the UI doesn't
	// claim every task appeared the instant the demo booted.
	firstSeen := oldestPerTask(specs, now)
	for i := range cfg.Tasks {
		name := cfg.Tasks[i].Name
		ts, ok := firstSeen[name]
		if !ok {
			ts = now
		}
		if err := db.EnsureTaskRegistered(ctx, name, ts); err != nil {
			return 0, fmt.Errorf("register task %q: %w", name, err)
		}
	}

	// Assign ULIDs single-threaded: the entropy reader is not safe for
	// concurrent use, and retries must be minted after their parent so the
	// ULID ordering matches the causal ordering.
	for _, s := range specs {
		s.run.ID = newULID(*s.run.StartAt, rng)
		s.run.CreatedAt = *s.run.StartAt
		s.logPath = logutil.ResolveRunLogPath(logDir, s.run.TaskName, s.run.ID, s.run.CreatedAt)
	}
	// Retries point at their parent by index; resolve now that IDs exist.
	for _, s := range specs {
		if s.retryOfIdx >= 0 {
			parentID := specs[s.retryOfIdx].run.ID
			s.run.RetryOfRunID = &parentID
		}
	}

	if err := writeAll(ctx, db, specs); err != nil {
		return 0, err
	}
	return len(specs), nil
}

// runSpec is one planned run plus everything needed to materialize it.
type runSpec struct {
	run        *model.Run
	task       *model.Task
	occ        time.Time
	outcome    outcome
	seed       int64
	logPath    string
	retryOfIdx int // index into specs of the parent run, or -1
}

type outcome struct {
	reason   model.EndReason
	exitCode int
}

var (
	outSuccess = outcome{model.ReasonSuccess, 0}
	outFailed  = outcome{model.ReasonFailed, 1}
	outTimeout = outcome{model.ReasonTimeout, 124}
	outCrashed = outcome{model.ReasonCrashed, 137}
)

// planRuns builds the full set of run specs (without IDs) for every task.
func planRuns(cfg *config.Config, now time.Time, rng *rand.Rand) []*runSpec {
	var specs []*runSpec
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		switch {
		case task.Kind.IsService():
			specs = append(specs, planService(task, cfg, now, rng)...)
		case task.Cron != "":
			specs = append(specs, planScheduled(task, cfg, now, rng)...)
		default:
			specs = append(specs, planManual(task, now, rng)...)
		}
	}
	// Top up with extra manual deploy runs if a future config edit drops the
	// total below the floor, so the demo never looks sparse.
	if len(specs) < minRuns {
		if t := findTask(cfg, "deploy-migrate"); t != nil {
			for n := len(specs); n < minRuns; n++ {
				off := time.Duration(rng.Int63n(int64(lookback)))
				specs = append(specs, newSpec(t, now.Add(-off), pickOutcome(rng, 90, 8, 0, 2), rng))
			}
		}
	}
	return specs
}

// planScheduled walks the task's cron backwards over the lookback window and
// emits one run per fire (capped), aligning history to the real schedule.
func planScheduled(task *model.Task, cfg *config.Config, now time.Time, rng *rand.Rand) []*runSpec {
	sched, err := cronParser().Parse(task.Cron)
	if err != nil {
		return nil
	}
	loc := taskLocation(task, cfg)
	fires := pastFires(sched, now.In(loc), lookback)

	limit := effectiveCap(task, cfg)
	if limit > maxPerScheduled {
		limit = maxPerScheduled
	}
	if len(fires) > limit {
		fires = fires[len(fires)-limit:]
	}

	specs := make([]*runSpec, 0, len(fires))
	for _, f := range fires {
		oc := pickOutcome(rng, 88, 7, 2, 3)
		spec := newSpec(task, f, oc, rng)
		specs = append(specs, spec)
		// Failed runs on a retrying task usually get a quick follow-up retry.
		if oc == outFailed && task.RetryAttempts > 0 && rng.Intn(100) < 70 {
			retry := newSpec(task, f.Add(retryGap(task)), pickOutcome(rng, 75, 20, 0, 5), rng)
			retry.run.RetryAttempt = 1
			retry.retryOfIdx = len(specs) - 1
			specs = append(specs, retry)
		}
	}
	return specs
}

// planManual scatters a handful of hand-triggered runs across recent history.
// The first few land on the canonical "5 minutes / 1 hour / 2 days ago" offsets
// the demo is meant to showcase.
func planManual(task *model.Task, now time.Time, rng *rand.Rand) []*runSpec {
	offsets := []time.Duration{
		5 * time.Minute, 70 * time.Minute, 5 * time.Hour,
		26 * time.Hour, 2 * 24 * time.Hour, 4 * 24 * time.Hour,
	}
	count := manualCount(task)
	specs := make([]*runSpec, 0, count)
	for i := 0; i < count; i++ {
		var off time.Duration
		if i < len(offsets) {
			off = offsets[i] + time.Duration(rng.Int63n(int64(3*time.Minute)))
		} else {
			off = time.Duration(rng.Int63n(int64(lookback)))
		}
		specs = append(specs, newSpec(task, now.Add(-off), pickOutcome(rng, 86, 9, 2, 3), rng))
	}
	return specs
}

// planService emits past restart runs for each instance: short crashes and
// longer clean restarts, all terminal (the daemon starts the live ones on boot).
func planService(task *model.Task, cfg *config.Config, now time.Time, rng *rand.Rand) []*runSpec {
	instances := task.Instances
	if instances < 1 {
		instances = 1
	}
	perInstance := 45
	var specs []*runSpec
	for inst := 0; inst < instances; inst++ {
		for i := 0; i < perInstance; i++ {
			off := time.Duration(rng.Int63n(int64(lookback)))
			oc := pickOutcome(rng, 80, 0, 0, 20) // services either restart cleanly or crash
			spec := newSpec(task, now.Add(-off), oc, rng)
			spec.run.InstanceIndex = inst
			specs = append(specs, spec)
		}
	}
	return specs
}

// newSpec builds a terminal run spec. End time is derived from a per-task
// typical duration (timeouts run right up to the task's timeout).
func newSpec(task *model.Task, start time.Time, oc outcome, rng *rand.Rand) *runSpec {
	dur := runDuration(task, oc, rng)
	end := start.Add(dur)
	run := &model.Run{
		TaskName:    task.Name,
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(oc.reason),
		ExitCode:    oc.exitCode,
		StartAt:     &start,
		EndAt:       &end,
		TriggeredBy: triggeredBy(task),
		CreatedAt:   start,
	}
	return &runSpec{
		run:        run,
		task:       task,
		occ:        start,
		outcome:    oc,
		seed:       rng.Int63(),
		retryOfIdx: -1,
	}
}

// writeAll materializes every spec: generate the log body, write the log file,
// then insert the run row. Fans out across CPUs; log generation dominates.
func writeAll(ctx context.Context, db seederDeps, specs []*runSpec) error {
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan *runSpec)
	errs := make(chan error, len(specs))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				if err := materialize(ctx, db, s); err != nil {
					errs <- err
				}
			}
		}()
	}
	for _, s := range specs {
		jobs <- s
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// materialize writes one run's log file (with finalized index/meta sidecars,
// via the real executor writer) and inserts its row.
func materialize(ctx context.Context, db seederDeps, s *runSpec) error {
	lines := buildLog(s, rand.New(rand.NewSource(s.seed)))

	if err := os.MkdirAll(filepath.Dir(s.logPath), 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	w, err := executor.NewLogWriter(executor.LogWriterOpts{
		LogPath:  s.logPath,
		IdxPath:  s.logPath + ".idx",
		TidxPath: s.logPath + ".tidx",
		Now:      lineClock(*s.run.StartAt, *s.run.EndAt, len(lines)),
	})
	if err != nil {
		return fmt.Errorf("open log writer: %w", err)
	}
	for _, ln := range lines {
		if _, werr := w.WriteLineEvent(ln.text, ln.stream); werr != nil {
			_ = w.Close()
			return fmt.Errorf("write log line: %w", werr)
		}
	}
	if cerr := w.Close(); cerr != nil {
		return fmt.Errorf("finalize log: %w", cerr)
	}

	if err := db.CreateRun(ctx, s.run); err != nil {
		return fmt.Errorf("insert run: %w", err)
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

func cronParser() cron.Parser {
	return cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
}

// pastFires returns the schedule's fire times within [now-window, now), oldest
// first. Mirrors the forward-stepping the catch-up logic uses.
func pastFires(sched cron.Schedule, now time.Time, window time.Duration) []time.Time {
	var fires []time.Time
	t := now.Add(-window)
	for {
		next := sched.Next(t)
		if !next.Before(now) {
			break
		}
		fires = append(fires, next)
		t = next
		if len(fires) > 200000 { // safety valve against a pathological schedule
			break
		}
	}
	return fires
}

func taskLocation(task *model.Task, cfg *config.Config) *time.Location {
	name := task.Timezone
	if name == "" {
		name = cfg.Scheduler.Timezone
	}
	loc, err := config.ResolveTimezone("demo", name)
	if err != nil || loc == nil {
		return time.UTC
	}
	return loc
}

// effectiveCap is how many recent runs to keep per task, leaving headroom under
// the task's keep_runs so the live runs the daemon adds on boot don't push the
// seeded history past retention and trim it away.
func effectiveCap(task *model.Task, cfg *config.Config) int {
	keep := task.KeepRuns
	if keep <= 0 {
		keep = cfg.Defaults.KeepRuns
	}
	if keep <= 0 {
		keep = 60
	}
	if keep <= 14 {
		return keep // tiny caps (e.g. weekly rotate keep_runs=12): use as-is
	}
	return keep - 10
}

// triggeredBy maps a task's kind/schedule to how its runs were started.
func triggeredBy(task *model.Task) model.TriggeredBy {
	switch {
	case task.Kind.IsService():
		return model.TriggeredByService
	case task.Cron != "":
		return model.TriggeredByCron
	default:
		return model.TriggeredByAPI
	}
}

func pickOutcome(rng *rand.Rand, success, failed, timeout, crashed int) outcome {
	n := rng.Intn(success + failed + timeout + crashed)
	switch {
	case n < success:
		return outSuccess
	case n < success+failed:
		return outFailed
	case n < success+failed+timeout:
		return outTimeout
	default:
		return outCrashed
	}
}

func manualCount(task *model.Task) int {
	switch task.Name {
	case "reindex-search":
		return 5
	case "import-legacy-data":
		return 4
	default:
		return 12
	}
}

func retryGap(task *model.Task) time.Duration {
	if task.RetryDelay > 0 {
		return task.RetryDelay
	}
	return 30 * time.Second
}

// runDuration returns a believable wall-clock duration for a run.
func runDuration(task *model.Task, oc outcome, rng *rand.Rand) time.Duration {
	if oc == outTimeout && task.Timeout > 0 {
		return task.Timeout
	}
	base := baseDuration(task)
	jitter := time.Duration(rng.Int63n(int64(base) + 1))
	d := base/2 + jitter
	if oc == outCrashed {
		d = d / 4 // crashes die early
	}
	if d <= 0 {
		d = time.Second
	}
	return d
}

func baseDuration(task *model.Task) time.Duration {
	switch task.Name {
	case "healthcheck-api", "healthcheck-tls", "disk-usage-report":
		return 2 * time.Second
	case "backup-postgres", "backup-restore-test":
		return 13 * time.Second
	case "reindex-search":
		return 5 * time.Second
	case "import-legacy-data":
		return 7 * time.Second
	case "weekly-billing-rollup":
		return 40 * time.Second
	}
	if task.Kind.IsService() {
		return 90 * time.Minute // service up-time between restarts
	}
	return 5 * time.Second
}

// oldestPerTask finds the earliest run start per task name for backdating
// task-registration "first seen" timestamps.
func oldestPerTask(specs []*runSpec, now time.Time) map[string]time.Time {
	out := make(map[string]time.Time)
	for _, s := range specs {
		ts := *s.run.StartAt
		if cur, ok := out[s.run.TaskName]; !ok || ts.Before(cur) {
			out[s.run.TaskName] = ts
		}
	}
	return out
}

func findTask(cfg *config.Config, name string) *model.Task {
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Name == name {
			return &cfg.Tasks[i]
		}
	}
	return nil
}

// newULID mints a backdated ULID whose embedded timestamp is the run's start,
// so the natural ULID ordering matches chronological order.
func newULID(t time.Time, entropy io.Reader) string {
	return ulid.MustNew(ulid.Timestamp(t), entropy).String()
}

// lineClock returns a clock that advances from start to end across total lines,
// so the timestamp index a long run produces spans the run's window.
func lineClock(start, end time.Time, total int) func() time.Time {
	span := end.Sub(start)
	if span < 0 {
		span = 0
	}
	n := 0
	return func() time.Time {
		if total <= 1 {
			return start
		}
		cur := n
		if cur > total-1 {
			cur = total - 1
		}
		n++
		return start.Add(time.Duration(int64(span) * int64(cur) / int64(total-1)))
	}
}
