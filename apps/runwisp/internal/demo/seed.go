// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/robfig/cron/v3"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
)

// minRuns is the floor on how many historical runs Seed produces; plan() tops
// up below it so the demo never looks sparse.
const minRuns = 500

const (
	// defaultLookback bounds how far back runs are scattered. 30 days stays
	// inside the config's default keep_for, so boot-time retention never trims a
	// seeded row.
	defaultLookback = 30 * 24 * time.Hour

	// defaultMaxPerScheduled caps fires per scheduled task so a `*/5` probe
	// doesn't alone write thousands of files, while still leaving a dense history.
	defaultMaxPerScheduled = 180

	// defaultServiceWindow bounds a service's infinite loop (which never exits on
	// its own) so execOne captures a representative sample instead of blocking
	// forever.
	defaultServiceWindow = 1200 * time.Millisecond

	// defaultServicePerInstance is how many past restart runs each service
	// instance gets.
	defaultServicePerInstance = 45
)

// seederDeps is the slice of storage the seeder needs. SQLiteDatabase satisfies
// it; tests can pass an in-memory database.
type seederDeps interface {
	CreateRun(ctx context.Context, run *model.Run) error
	EnsureTaskRegistered(ctx context.Context, taskName string, firstSeen time.Time) error
}

// seedOptions tunes the planning/execution knobs. Production uses the defaults
// (see Seed); tests pass small values so a synthetic config seeds fast.
type seedOptions struct {
	lookback           time.Duration
	maxPerScheduled    int
	serviceWindow      time.Duration
	servicePerInstance int
}

// Seed populates the database and on-disk log directory with believable
// historical runs for every task in cfg, then returns how many it created.
//
// Each command runs through the real executor under a backdated clock: captured
// output is the log, exit code is the outcome, nothing fabricated. Planning is
// single-threaded (order-sensitive); subprocess execution fans out across CPUs.
func Seed(ctx context.Context, db seederDeps, cfg *config.Config, logDir string, now time.Time) (int, error) {
	return seedWith(ctx, db, cfg, logDir, now, seedOptions{
		lookback:           defaultLookback,
		maxPerScheduled:    defaultMaxPerScheduled,
		serviceWindow:      defaultServiceWindow,
		servicePerInstance: defaultServicePerInstance,
	})
}

// seedWith is Seed with explicit knobs, so tests can drive a tiny config over a
// short lookback without the full 30-day real-execution cost.
func seedWith(ctx context.Context, db seederDeps, cfg *config.Config, logDir string, now time.Time, opts seedOptions) (int, error) {
	s := &seeder{db: db, cfg: cfg, logDir: logDir, now: now, opts: opts}
	return s.run(ctx)
}

// seeder owns one seeding pass: the config, target paths, the reference "now",
// and the tuning knobs.
type seeder struct {
	db     seederDeps
	cfg    *config.Config
	logDir string
	now    time.Time
	opts   seedOptions
}

// run plans the primaries, executes them, then follows the genuine failures
// with honest retry runs.
func (s *seeder) run(ctx context.Context) (int, error) {
	rng := rand.New(rand.NewSource(s.now.UnixNano()))

	specs := s.plan(rng)

	// Backdate each task's "first seen" to its oldest run so the UI doesn't
	// claim every task appeared the instant the demo booted.
	firstSeen := oldestPerTask(specs)
	for i := range s.cfg.Tasks {
		name := s.cfg.Tasks[i].Name
		ts, ok := firstSeen[name]
		if !ok {
			ts = s.now
		}
		if err := s.db.EnsureTaskRegistered(ctx, name, ts); err != nil {
			return 0, fmt.Errorf("register task %q: %w", name, err)
		}
	}

	// Mint backdated ULIDs single-threaded before the parallel pass: the
	// entropy reader is not safe for concurrent use, and a run's ULID embeds
	// its start so natural ULID ordering matches chronological order.
	for _, spec := range specs {
		spec.run.ID = newULID(*spec.run.StartAt, rng)
	}

	if err := s.runAll(ctx, specs); err != nil {
		return 0, err
	}

	// Honest retry pass: now that every primary carries a real outcome, follow
	// up the runs that genuinely failed on a retrying cron task.
	retries := s.planRetries(specs, rng)
	if err := s.runAll(ctx, retries); err != nil {
		return 0, err
	}

	return len(specs) + len(retries), nil
}

// runSpec is one planned run plus the task it executes. forceReason overrides
// the reason derived from the real run; it is non-nil only for the crashed
// fraction of service restarts, which the executor can't infer from a bounded
// sample window.
type runSpec struct {
	run         *model.Run
	task        *model.Task
	forceReason *model.EndReason
}

// plan builds the full set of run specs (without IDs) for every task.
func (s *seeder) plan(rng *rand.Rand) []*runSpec {
	var specs []*runSpec
	for i := range s.cfg.Tasks {
		task := &s.cfg.Tasks[i]
		switch {
		case task.Kind.IsService():
			specs = append(specs, s.planService(task, rng)...)
		case task.Cron != "":
			specs = append(specs, s.planScheduled(task)...)
		default:
			specs = append(specs, s.planManual(task, rng)...)
		}
	}
	// Top up with extra manual deploy runs if a future config edit drops the
	// total below the floor, so the demo never looks sparse.
	if len(specs) < minRuns {
		if t := findTask(s.cfg, "deploy-migrate"); t != nil {
			for n := len(specs); n < minRuns; n++ {
				off := time.Duration(rng.Int63n(int64(s.opts.lookback)))
				specs = append(specs, newSpec(t, s.now.Add(-off)))
			}
		}
	}
	return specs
}

// planScheduled walks the task's cron backwards over the lookback window and
// emits one run per fire (capped), aligning history to the real schedule.
func (s *seeder) planScheduled(task *model.Task) []*runSpec {
	sched, err := cronParser().Parse(task.Cron)
	if err != nil {
		return nil
	}
	loc := taskLocation(task, s.cfg)
	fires := pastFires(sched, s.now.In(loc), s.opts.lookback)

	limit := effectiveCap(task, s.cfg)
	if limit > s.opts.maxPerScheduled {
		limit = s.opts.maxPerScheduled
	}
	if len(fires) > limit {
		fires = fires[len(fires)-limit:]
	}

	specs := make([]*runSpec, 0, len(fires))
	for _, f := range fires {
		specs = append(specs, newSpec(task, f))
	}
	return specs
}

// planManual scatters a handful of hand-triggered runs across recent history.
// The first few land on the canonical "5 minutes / 1 hour / 2 days ago" offsets
// the demo is meant to showcase.
func (s *seeder) planManual(task *model.Task, rng *rand.Rand) []*runSpec {
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
			off = time.Duration(rng.Int63n(int64(s.opts.lookback)))
		}
		specs = append(specs, newSpec(task, s.now.Add(-off)))
	}
	return specs
}

// planService emits past restart runs for each instance. Services are infinite
// loops, so their terminal reason is assigned, not derived: most are clean
// cycles (Stopped, from the real cancellation), a fraction are forced Crashed.
func (s *seeder) planService(task *model.Task, rng *rand.Rand) []*runSpec {
	instances := task.Instances
	if instances < 1 {
		instances = 1
	}
	var specs []*runSpec
	for inst := 0; inst < instances; inst++ {
		for i := 0; i < s.opts.servicePerInstance; i++ {
			off := time.Duration(rng.Int63n(int64(s.opts.lookback)))
			spec := newSpec(task, s.now.Add(-off))
			spec.run.InstanceIndex = inst
			if rng.Intn(100) < 20 {
				spec.forceReason = model.EndReasonPtr(model.ReasonCrashed)
			}
			specs = append(specs, spec)
		}
	}
	return specs
}

// planRetries follows each genuinely-failed primary on a retrying cron task with
// one linked retry run, minting its ULID after the parent so ULID order matches
// causal order.
func (s *seeder) planRetries(primaries []*runSpec, rng *rand.Rand) []*runSpec {
	var retries []*runSpec
	for _, p := range primaries {
		if !retryEligible(p) {
			continue
		}
		start := p.run.EndAt.Add(retryGap(p.task))
		if start.After(s.now) {
			continue // keep every seeded row backdated
		}
		retry := newSpec(p.task, start)
		retry.run.RetryAttempt = 1
		retry.run.ID = newULID(start, rng)
		parentID := p.run.ID
		retry.run.RetryOfRunID = &parentID
		retries = append(retries, retry)
	}
	return retries
}

// retryEligible reports whether a primary's real outcome warrants a seeded
// retry: a non-service cron task that allows retries and really did not succeed.
func retryEligible(p *runSpec) bool {
	return p.task.Cron != "" &&
		!p.task.Kind.IsService() &&
		p.task.RetryAttempts > 0 &&
		p.run.RetryOfRunID == nil &&
		p.run.IsRetryable()
}

// newSpec builds a running run anchored at start. EndAt/EndReason are filled in
// by execOne once the real command has run.
func newSpec(task *model.Task, start time.Time) *runSpec {
	return &runSpec{
		run: &model.Run{
			TaskName:    task.Name,
			Status:      model.PhaseRunning,
			StartAt:     &start,
			CreatedAt:   start,
			TriggeredBy: triggeredBy(task),
		},
		task: task,
	}
}

// runAll executes every spec through the real executor, fanning out across CPUs
// (subprocess execution dominates), then inserts each run row.
func (s *seeder) runAll(ctx context.Context, specs []*runSpec) error {
	if len(specs) == 0 {
		return nil
	}
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
			for spec := range jobs {
				if err := s.execOne(ctx, spec); err != nil {
					errs <- err
				}
			}
		}()
	}
	for _, spec := range specs {
		jobs <- spec
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

// execOne runs one spec's real command through the executor with a backdated
// clock, derives the terminal reason from the result, and inserts the row.
func (s *seeder) execOne(ctx context.Context, spec *runSpec) error {
	start := *spec.run.StartAt

	// Backdated, monotonic clock: captured log lines carry the historical run
	// date while still advancing in real time as the process emits them.
	var t0 time.Time
	clk := func() time.Time { return start.Add(time.Since(t0)) }

	exec := executor.New(executor.Options{
		LogDir:        s.logDir,
		HasLocalTasks: true,
		MinFreeDisk:   0,
		Clock:         clk,
	})

	runCtx, cancel := s.runContext(ctx, spec.task)
	defer cancel()

	t0 = time.Now()
	res := exec.Execute(runCtx, spec.task, spec.run) // writes the real log + finalized sidecars

	reason := res.EndReason()
	if spec.forceReason != nil {
		reason = *spec.forceReason
	}

	end := start.Add(time.Since(t0))
	if spec.task.Kind.IsService() {
		// The captured log only spans the sample window; the row shows a fuller
		// uptime, matching how a long-lived service's rotated log looks.
		end = start.Add(serviceUptime(spec))
		if end.After(s.now) {
			end = s.now
		}
	}
	spec.run.End(reason, res.ExitCode, end)
	return s.db.CreateRun(ctx, spec.run)
}

// runContext bounds a run the way the manager does: non-services get
// task.Timeout, so a run that blows its budget really times out. A service's
// infinite loop is cancelled by a short sample window, which the executor
// reports as Stopped — a clean instance cycle, not a timeout.
func (s *seeder) runContext(ctx context.Context, task *model.Task) (context.Context, context.CancelFunc) {
	if task.Kind.IsService() {
		runCtx, cancel := context.WithCancel(ctx)
		timer := time.AfterFunc(s.opts.serviceWindow, cancel)
		return runCtx, func() { timer.Stop(); cancel() }
	}
	if task.Timeout > 0 {
		return context.WithTimeout(ctx, task.Timeout)
	}
	return context.WithCancel(ctx)
}

// serviceUptime returns a believable uptime for a seeded service instance run.
// Derived from the run's start (no rng: execOne runs in parallel and a
// math/rand source is not safe to share); a crashed instance died early.
func serviceUptime(spec *runSpec) time.Duration {
	const base = 90 * time.Minute
	jitter := time.Duration(spec.run.StartAt.UnixNano() % int64(base))
	d := base/2 + jitter // 45–135 min
	if spec.forceReason != nil && *spec.forceReason == model.ReasonCrashed {
		d /= 4
	}
	return d
}

// --- helpers -----------------------------------------------------------------

func cronParser() cron.Parser {
	return cronspec.NewParser()
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

// oldestPerTask finds the earliest run start per task name for backdating
// task-registration "first seen" timestamps.
func oldestPerTask(specs []*runSpec) map[string]time.Time {
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
