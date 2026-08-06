// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
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
// without contention when cloudWG is already done and srv is nil.
func TestWaitInput_ImmediateReturnWhenNothingPending(t *testing.T) {
	var cloudWG sync.WaitGroup // already at zero
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	waitInput(ctx, &cloudWG, nil)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("waitInput took too long with no work: %v", elapsed)
	}
}

func TestStartCloudClient_DisabledReturnsZeroWG(t *testing.T) {
	cfg := &daemonConfig{}
	cfg.CloudConfig.Enabled = false

	cancelCloud, wg := startCloudClient(context.Background(), cfg, &daemonServices{}, nil)
	if cancelCloud == nil {
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
		t.Fatal("expected wg.Wait() to return immediately with disabled cloud")
	}
	cancelCloud()
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
	exec := initExecutor(cfg, bus, f.LogDir(), "")
	dc := &daemonConfig{Config: cfg}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)
	tasks := runtime.NewTaskRegistry(tasksMap)

	cleaner := initRetentionCleaner(dc, db, tasks, f.LogDir())
	purger := runtime.NewSoftDeletePurger(db, f.LogDir())
	purger.Start()

	return &daemonServices{
		DB:                  db,
		EventBus:            bus,
		Executor:            exec,
		TaskManager:         tm,
		Tasks:               tasks,
		RetentionCleaner:    cleaner,
		SoftDeletePurger:    purger,
		TaskShutdownTimeout: 100 * time.Millisecond,
	}
}

func TestWaitDrain_NilSchedulerAndNotifyReturnsPromptly(t *testing.T) {
	svc := minimalServices(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	waitDrain(ctx, svc, 100*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("waitDrain took too long with empty TaskManager: %v", elapsed)
	}
}

func TestGracefulShutdown_NoSrvNoCloud(t *testing.T) {
	svc := minimalServices(t)

	cancelCloud := func() {}
	var cloudWG sync.WaitGroup // already zero

	start := time.Now()
	gracefulShutdown(cancelCloud, &cloudWG, svc, nil)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("gracefulShutdown took too long with idle services: %v", elapsed)
	}
}

func TestGracefulShutdown_AppliesFallbackTimeoutWhenUnset(t *testing.T) {
	svc := minimalServices(t)
	// 0 forces the helper down the fallback-timeout branch.
	svc.TaskShutdownTimeout = 0

	cancelCloud := func() {}
	var cloudWG sync.WaitGroup

	gracefulShutdown(cancelCloud, &cloudWG, svc, nil)
}

func TestGracefulShutdown_WithScheduler(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "")
	dc := &daemonConfig{Config: cfg}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)
	tasks := runtime.NewTaskRegistry(tasksMap)

	loc, _ := config.ResolveTimezone("scheduler.timezone", "UTC")
	scheduler := runtime.NewScheduler(tm, tasksMap, loc, nil)
	_, _ = scheduler.Start()

	svc := &daemonServices{
		DB:                  db,
		EventBus:            bus,
		Executor:            exec,
		TaskManager:         tm,
		Tasks:               tasks,
		Scheduler:           scheduler,
		RetentionCleaner:    initRetentionCleaner(dc, db, tasks, f.LogDir()),
		TaskShutdownTimeout: 100 * time.Millisecond,
	}

	cancelCloud := func() {}
	var cloudWG sync.WaitGroup

	gracefulShutdown(cancelCloud, &cloudWG, svc, nil)
}

func TestRunHeadless_ExitsOnSignal(t *testing.T) {
	svc := minimalServices(t)

	// runDaemon owns the signal channel in production. Tests supply their own
	// pre-armed channel so runHeadless never races to install Notify.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	rt := &daemonRuntime{
		sigCh:       sigCh,
		svc:         svc,
		cancelCloud: func() {},
		cloudWG:     &sync.WaitGroup{},
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
