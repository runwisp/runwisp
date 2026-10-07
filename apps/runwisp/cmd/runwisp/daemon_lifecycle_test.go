// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAwaitOrLog_ReturnsWhenWaitGroupCompletes asserts the helper returns
// promptly once the work it is waiting on finishes, without hitting the
// deadline branch.
func TestAwaitOrLog_ReturnsWhenWaitGroupCompletes(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		time.Sleep(5 * time.Millisecond)
		wg.Done()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	awaitOrLog(ctx, &wg, "test")
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("awaitOrLog took too long: %v", elapsed)
	}
}

// TestAwaitOrLog_ReturnsOnContextDeadline asserts the helper returns when the
// caller's deadline passes, even if the work is still pending. The slog.Warn
// is non-observable here, but the function must not block.
func TestAwaitOrLog_ReturnsOnContextDeadline(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	// Never Done — the work outlives our deadline.
	defer wg.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	awaitOrLog(ctx, &wg, "test")
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Fatalf("awaitOrLog overshot deadline by too much: %v", elapsed)
	}
	if elapsed < 15*time.Millisecond {
		t.Fatalf("awaitOrLog returned before deadline: %v", elapsed)
	}
}

// TestWaitInput_ImmediateReturnWhenNothingPending asserts waitInput returns
// without contention when stationWG is already done and srv is nil.
func TestWaitInput_ImmediateReturnWhenNothingPending(t *testing.T) {
	var stationWG sync.WaitGroup // already at zero
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	waitInput(ctx, &stationWG, nil)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("waitInput took too long with no work: %v", elapsed)
	}
}

// TestRequestSelfRestart_RejectsWithoutStationDispatchOptIn is the bug-first
// regression for agent:restart bypassing allow_station_dispatch: restarting the
// daemon process is at least as sensitive as the ad-hoc dispatch that flag
// already gates (see internal/station/dispatch_resolver.go), so it must be
// refused the same way — regardless of whether the daemon happens to be
// service-managed. The dispatch check runs before the service-manager check,
// so this is exercised without depending on the test host's environment.
func TestRequestSelfRestart_RejectsWithoutStationDispatchOptIn(t *testing.T) {
	err := requestSelfRestart(false, nil)
	if err == nil {
		t.Fatal("expected requestSelfRestart(false) to be rejected")
	}
	const want = "station dispatch disabled (set [daemon] allow_station_dispatch = true to enable)"
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}

func TestStartStationClient_DisabledReturnsZeroWG(t *testing.T) {
	cfg := &daemonConfig{}
	cfg.StationConfig.Enabled = false

	cancelStation, wg := startStationClient(context.Background(), cfg, &daemonServices{}, nil, nil)
	if cancelStation == nil {
		t.Fatal("expected non-nil cancel func")
	}
	if wg == nil {
		t.Fatal("expected non-nil wg")
	}
	// wg should be at zero; Wait should return immediately.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected wg.Wait() to return immediately with disabled station")
	}
	cancelStation()
}

// minimalServices wires up a daemonServices with only the bits required by
// the shutdown helpers — empty TaskManager (no active runs), real
// RetentionCleaner / SoftDeletePurger so .Stop() is safe, and nil scheduler /
// notify so those branches are exercised harmlessly.
func minimalServices(t *testing.T) *daemonServices {
	t.Helper()
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "", nil)
	dc := &daemonConfig{Config: cfg}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)
	tasks := runtime.NewTaskRegistry(tasksMap)

	cleaner := initRetentionCleaner(dc, db, tasks, f.LogDir(), bus)
	purger := runtime.NewSoftDeletePurger(db, f.LogDir())
	purger.Start()

	svc := &daemonServices{
		DB:               db,
		EventBus:         bus,
		Executor:         exec,
		TaskManager:      tm,
		Tasks:            tasks,
		RetentionCleaner: cleaner,
		SoftDeletePurger: purger,
	}
	svc.TaskShutdownTimeout.Store(int64(100 * time.Millisecond))
	return svc
}

func TestWaitDrain_NilSchedulerAndNotifyReturnsPromptly(t *testing.T) {
	svc := minimalServices(t)

	start := time.Now()
	waitDrain(svc, 100*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("waitDrain took too long with empty TaskManager: %v", elapsed)
	}
}

// slowShutdownManager finishes ShutdownWithDeadline a little after its own
// deadline, like a manager that has to SIGKILL and reap a stuck run.
type slowShutdownManager struct {
	runtime.TaskManager
	finished atomic.Bool
}

func (m *slowShutdownManager) ShutdownWithDeadline(deadline time.Duration) {
	time.Sleep(deadline + 100*time.Millisecond)
	m.finished.Store(true)
}

// TestGracefulShutdown_WaitsForTaskManagerKill: the manager's deadline and the
// daemon's outer wait used to be the same length, so the daemon returned (and
// exited) before the manager had force-killed its survivors.
func TestGracefulShutdown_WaitsForTaskManagerKill(t *testing.T) {
	svc := minimalServices(t)
	slow := &slowShutdownManager{TaskManager: svc.TaskManager}
	svc.TaskManager = slow

	var stationWG sync.WaitGroup
	gracefulShutdown(func() {}, &stationWG, svc, nil)

	if !slow.finished.Load() {
		t.Fatal("gracefulShutdown returned before the task manager finished its force-kill")
	}
}

// stuckServiceManager has a service that never stops on its own.
type stuckServiceManager struct{ slowShutdownManager }

func (m *stuckServiceManager) StopService(string) error     { return nil }
func (m *stuckServiceManager) GetActiveRunCount(string) int { return 1 }

// TestGracefulShutdown_SlowServiceStopStillWaitsForKill: stopping services
// first used to spend the outer deadline, so with a service that ignored its
// stop signal the daemon exited before the task manager's kill.
func TestGracefulShutdown_SlowServiceStopStillWaitsForKill(t *testing.T) {
	svc := minimalServices(t)
	stuck := &stuckServiceManager{slowShutdownManager{TaskManager: svc.TaskManager}}
	svc.TaskManager = stuck
	svc.Tasks = runtime.NewTaskRegistry(map[string]*model.Task{"web": {Name: "web", Kind: model.KindService}})

	var stationWG sync.WaitGroup
	gracefulShutdown(func() {}, &stationWG, svc, nil)

	if !stuck.finished.Load() {
		t.Fatal("gracefulShutdown returned before the task manager finished its force-kill")
	}
}

func TestGracefulShutdown_NoSrvNoStation(t *testing.T) {
	svc := minimalServices(t)

	cancelStation := func() {}
	var stationWG sync.WaitGroup // already zero

	start := time.Now()
	gracefulShutdown(cancelStation, &stationWG, svc, nil)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("gracefulShutdown took too long with idle services: %v", elapsed)
	}
}

func TestGracefulShutdown_AppliesFallbackTimeoutWhenUnset(t *testing.T) {
	svc := minimalServices(t)
	// 0 forces the helper down the fallback-timeout branch.
	svc.TaskShutdownTimeout.Store(0)

	cancelStation := func() {}
	var stationWG sync.WaitGroup

	gracefulShutdown(cancelStation, &stationWG, svc, nil)
}

func TestGracefulShutdown_WithScheduler(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "", nil)
	dc := &daemonConfig{Config: cfg}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)
	tasks := runtime.NewTaskRegistry(tasksMap)

	loc, _ := config.ResolveTimezone("daemon.timezone", "UTC")
	scheduler := runtime.NewScheduler(tm, tasksMap, loc, nil)
	_, _ = scheduler.Start()

	svc := &daemonServices{
		DB:               db,
		EventBus:         bus,
		Executor:         exec,
		TaskManager:      tm,
		Tasks:            tasks,
		Scheduler:        scheduler,
		RetentionCleaner: initRetentionCleaner(dc, db, tasks, f.LogDir(), bus),
	}
	svc.TaskShutdownTimeout.Store(int64(100 * time.Millisecond))

	cancelStation := func() {}
	var stationWG sync.WaitGroup

	gracefulShutdown(cancelStation, &stationWG, svc, nil)
}

func TestRunHeadless_ExitsOnSignal(t *testing.T) {
	svc := minimalServices(t)

	// runDaemon owns the signal channel in production. Tests supply their own
	// pre-armed channel so runHeadless never races to install Notify.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	rt := &daemonRuntime{
		sigCh:         sigCh,
		svc:           svc,
		cancelStation: func() {},
		stationWG:     &sync.WaitGroup{},
	}

	done := make(chan error, 1)
	go func() {
		done <- runHeadless(rt)
	}()

	if err := sendSelfSIGTERM(); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runHeadless returned err: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runHeadless did not return after SIGTERM")
	}

	// Recreate ignored tasks aren't necessary; svc was minimalServices and
	// gracefulShutdown already tore it down.
	_ = model.Task{}
}

// runHeadlessUntil runs runHeadless against idle services, fires trigger once
// it is waiting on signals, and returns what it exits with: runDaemon's return
// value, so non-nil here is exit status 1.
func runHeadlessUntil(t *testing.T, rt *daemonRuntime, trigger func()) error {
	t.Helper()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	rt.sigCh = sigCh
	rt.svc = minimalServices(t)
	rt.cancelStation = func() {}
	rt.stationWG = &sync.WaitGroup{}

	done := make(chan error, 1)
	go func() { done <- runHeadless(rt) }()
	trigger()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("runHeadless did not return")
		return nil
	}
}

// A fatal server error used to shut the daemon down with exit 0, so systemd's
// Restart=on-failure never restarted it and OnFailure= never fired.
func TestRunHeadless_FatalServerErrorExitsNonZero(t *testing.T) {
	fatalCh := make(chan error, 1)
	bindErr := errors.New("listen unix runwisp.sock: bind: invalid argument")
	err := runHeadlessUntil(t, &daemonRuntime{fatalCh: fatalCh}, func() {
		if err := exitNonZero(fatalCh, bindErr); err != nil {
			t.Error(err)
		}
	})
	assert.ErrorIs(t, err, bindErr)
}

// Same for a panic crashguard recovers in a guarded goroutine: a real Guard,
// the real latch.
func TestRunHeadless_GuardedPanicExitsNonZero(t *testing.T) {
	err := runHeadlessUntil(t, &daemonRuntime{panicked: crashguard.Panicked}, func() {
		go func() {
			defer crashguard.Guard()
			panic("boom")
		}()
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

// An agent:restart has to exit non-zero too: systemd and launchd only bring
// back a daemon that did not exit cleanly, so an exit 0 stopped the agent for
// good instead of restarting it.
func TestRequestSelfRestart_ExitsNonZeroForServiceManager(t *testing.T) {
	t.Setenv(autostart.ServiceManagedEnv, "1")
	fatalCh := make(chan error, 1)
	err := runHeadlessUntil(t, &daemonRuntime{fatalCh: fatalCh}, func() {
		if err := requestSelfRestart(true, fatalCh); err != nil {
			t.Error(err)
		}
	})
	assert.ErrorIs(t, err, errRestartRequested)
}
