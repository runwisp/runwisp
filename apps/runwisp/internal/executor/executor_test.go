// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLogPath returns a function that, after Execute completes, yields the
// log path published on the run.updated event envelope. The executor no
// longer stamps LogPath on the Run row; tests read it off the event payload.
func captureLogPath(eb events.EventBus) func() string {
	var (
		mu      sync.Mutex
		logPath string
	)
	eb.Subscribe(events.EventRunUpdated, func(e events.Event) {
		re, ok := e.Data.(events.RunEvent)
		if !ok || re.LogPath == "" {
			return
		}
		mu.Lock()
		logPath = re.LogPath
		mu.Unlock()
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return logPath
	}
}

func TestExecuteSuccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})
	getLogPath := captureLogPath(eb)

	task := &model.Task{
		Name: "test-task",
		Run:  "echo 'hello world'",
	}
	run := &model.Run{
		ID:     ulid.Make().String(),
		Status: model.PhaseRunning,
	}

	result := exec.Execute(context.Background(), task, run)
	assert.Equal(t, 0, result.ExitCode)
	assert.NoError(t, result.Error)

	// Check log file
	logContent, err := os.ReadFile(getLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(logContent), "hello world")
}

func TestExecuteFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})

	task := &model.Task{
		Name: "fail-task",
		Run:  "exit 1",
	}
	run := &model.Run{
		ID:     ulid.Make().String(),
		Status: model.PhaseRunning,
	}

	result := exec.Execute(context.Background(), task, run)
	assert.Equal(t, 1, result.ExitCode)
}

func TestExecuteTimeout(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})

	task := &model.Task{
		Name: "sleep-task",
		Run:  "sleep 2",
	}
	run := &model.Run{
		ID:     ulid.Make().String(),
		Status: model.PhaseRunning,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := exec.Execute(ctx, task, run)
	assert.NotEqual(t, 0, result.ExitCode) // Should be killed
}

func TestExecuteStderr(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})
	getLogPath := captureLogPath(eb)

	task := &model.Task{
		Name: "stderr-task",
		Run:  "echo 'error message' >&2",
	}
	run := &model.Run{
		ID:     ulid.Make().String(),
		Status: model.PhaseRunning,
	}

	result := exec.Execute(context.Background(), task, run)
	assert.Equal(t, 0, result.ExitCode)

	logContent, err := os.ReadFile(getLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(logContent), "error message")
	assert.Contains(t, string(logContent), "[ERR]")
}

func TestExecuteEvents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})

	var receivedLogs strings.Builder
	var mu sync.Mutex
	unsub := eb.Subscribe(events.EventLogLine, func(e events.Event) {
		if logEvent, ok := e.Data.(events.LogLineEvent); ok {
			mu.Lock()
			receivedLogs.WriteString(logEvent.Text)
			receivedLogs.WriteString("\n")
			mu.Unlock()
		}
	})
	defer unsub()

	task := &model.Task{
		Name: "event-task",
		Run:  "echo 'line 1'\necho 'line 2'",
	}
	run := &model.Run{
		ID:     ulid.Make().String(),
		Status: model.PhaseRunning,
	}

	exec.Execute(context.Background(), task, run)

	// Wait a bit for async events
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	logs := receivedLogs.String()
	mu.Unlock()

	assert.Contains(t, logs, "line 1")
	assert.Contains(t, logs, "line 2")
}

func TestRunUpdateCallback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	eb := events.NewEventBus()
	called := false
	exec := New(Options{
		LogDir:               tmpDir,
		EventBus:             eb,
		CloudDispatchEnabled: true,
		HasLocalTasks:        true,
		OnRunUpdate: func(r *model.Run) {
			called = true
		},
	})
	getLogPath := captureLogPath(eb)

	task := &model.Task{
		Name: "callback-task",
		Run:  "echo hi",
	}
	run := &model.Run{
		ID:     ulid.Make().String(),
		Status: model.PhaseRunning,
	}

	exec.Execute(context.Background(), task, run)
	assert.True(t, called)
	assert.NotEmpty(t, getLogPath(), "executor must publish LogPath on the run.updated event")
}

func TestLogDirCreationFailure(t *testing.T) {
	// Use a file as logDir to force mkdir failure
	tmpFile, err := os.CreateTemp("", "executor-test-file")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpFile.Name(), EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})

	task := &model.Task{Name: "fail", Run: "echo hi"}
	run := &model.Run{ID: ulid.Make().String()}

	result := exec.Execute(context.Background(), task, run)
	assert.Equal(t, -1, result.ExitCode)
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "failed to create log directory")
}

func TestLogFileCreationFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Make dir read-only
	os.Chmod(tmpDir, 0500)

	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})

	task := &model.Task{Name: "fail", Run: "echo hi"}
	run := &model.Run{ID: ulid.Make().String()}

	result := exec.Execute(context.Background(), task, run)
	// If running as root (e.g. in some containers), chmod might not stop root.
	// But assuming normal user.
	if os.Geteuid() != 0 {
		assert.Equal(t, -1, result.ExitCode)
		assert.Error(t, result.Error)
		assert.Contains(t, result.Error.Error(), "create task log dir")
	}

}

func TestExecuteCommandStartFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executor-test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})

	// A bare name that PATH lookup misses reports 127 ("command not found") on
	// every shell we arm errexit for. An absolute path that does not exist is
	// less portable: dash still reports 127, but bash-as-/bin/sh collapses it to
	// 1 under -e, so all we can pin there is "the run failed".
	t.Run("missing command on PATH reports 127", func(t *testing.T) {
		task := &model.Task{Name: "fail-start", Run: "nonexistent_runwisp_command"}
		run := &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}

		result := exec.Execute(context.Background(), task, run)

		assert.Equal(t, 127, result.ExitCode)
	})

	t.Run("missing absolute path fails the run", func(t *testing.T) {
		task := &model.Task{Name: "fail-start-abs", Run: "/path/to/non/existent/command"}
		run := &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}

		result := exec.Execute(context.Background(), task, run)

		assert.NotEqual(t, 0, result.ExitCode)
	})
}

// recordingBackend is a Backend that records the def it was asked to start and
// returns a no-op process that exits cleanly. It lets routing tests assert the
// executor dispatched to the right backend without spawning anything.
type recordingBackend struct {
	mu      sync.Mutex
	started int
	lastDef model.ExecutionDef
}

func (b *recordingBackend) Start(_ context.Context, _ *model.Task, _ *model.Run, def model.ExecutionDef) (*Process, error) {
	b.mu.Lock()
	b.started++
	b.lastDef = def
	b.mu.Unlock()
	return &Process{
		Wait: func() (int, error) { return 0, nil },
	}, nil
}

func (b *recordingBackend) Available(context.Context) bool { return true }

func newComposeTask(name string) *model.Task {
	return &model.Task{
		Name:         name,
		ExecutionDef: &model.ComposeExecution{File: "./docker-compose.yml", Service: "web", Mode: model.ComposeModeServices},
	}
}

// TestExecuteRoutesComposeToComposeBackend is the critical routing test: a task
// whose resolved execution def is a *model.ComposeExecution must reach the
// backend registered under Options.Compose.
func TestExecuteRoutesComposeToComposeBackend(t *testing.T) {
	tmpDir := t.TempDir()
	eb := events.NewEventBus()
	be := &recordingBackend{}
	exec := New(Options{LogDir: tmpDir, EventBus: eb, Compose: be, HasLocalTasks: true})

	run := &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}
	result := exec.Execute(context.Background(), newComposeTask("compose-route"), run)

	require.NoError(t, result.Error)
	assert.Equal(t, 0, result.ExitCode)
	be.mu.Lock()
	defer be.mu.Unlock()
	assert.Equal(t, 1, be.started, "compose backend should be started exactly once")
	_, ok := be.lastDef.(*model.ComposeExecution)
	assert.True(t, ok, "backend should receive the ComposeExecution def")
}

func TestAvailabilityReflectsComposeOption(t *testing.T) {
	tmpDir := t.TempDir()
	eb := events.NewEventBus()

	// Backend presence only governs compose availability once dispatch is enabled.
	withCompose := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, Compose: &recordingBackend{}, HasLocalTasks: true})
	assert.True(t, withCompose.Availability().Compose.Available)

	withoutCompose := New(Options{LogDir: tmpDir, EventBus: eb, CloudDispatchEnabled: true, HasLocalTasks: true})
	status := withoutCompose.Availability().Compose
	assert.False(t, status.Available)
	assert.Contains(t, status.Reason, "docker compose CLI unavailable")
}

// TestExecuteComposeWithoutBackendIsUnsupported covers the negative routing
// path: when no compose backend is registered, a ComposeExecution task fails
// with an "unsupported execution type" error rather than silently no-op'ing.
func TestExecuteComposeWithoutBackendIsUnsupported(t *testing.T) {
	tmpDir := t.TempDir()
	eb := events.NewEventBus()
	exec := New(Options{LogDir: tmpDir, EventBus: eb, HasLocalTasks: true})

	run := &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}
	result := exec.Execute(context.Background(), newComposeTask("compose-missing"), run)

	require.Error(t, result.Error)
	assert.Equal(t, -1, result.ExitCode)
	assert.Contains(t, result.Error.Error(), "unsupported execution type: compose")
}

func newTestExecutor(t *testing.T, opts Options) *RoutingExecutor {
	t.Helper()
	if opts.EventBus == nil {
		opts.EventBus = events.NewEventBus()
	}
	if opts.LogDir == "" {
		opts.LogDir = t.TempDir()
	}
	e, ok := New(opts).(*RoutingExecutor)
	require.True(t, ok, "New must return *RoutingExecutor")
	return e
}

func TestRoutingExecutor_Availability_DefaultsHTTPOnly(t *testing.T) {
	e := newTestExecutor(t, Options{})
	avail := e.Availability()

	assert.True(t, avail.HTTP.Available, "HTTP dispatch is allowed without the opt-in")
	assert.False(t, avail.Shell.Available, "shell defaults to unavailable when cloud dispatch is disabled")
	assert.NotEmpty(t, avail.Shell.Reason)
	assert.False(t, avail.Container.Available, "container defaults to unavailable when cloud dispatch is disabled")
	assert.False(t, avail.Compose.Available, "compose defaults to unavailable when cloud dispatch is disabled")
	assert.False(t, avail.Config.Available, "no local tasks declared")
}

func TestRoutingExecutor_Availability_CloudDispatchEnabled(t *testing.T) {
	e := newTestExecutor(t, Options{CloudDispatchEnabled: true})
	assert.True(t, e.Availability().Shell.Available)
}

// TestRoutingExecutor_Availability_ContainerGatedOnDispatch proves the opt-in,
// not merely backend presence, governs container dispatch: a backend is present
// in both cases, yet container is unavailable until cloud dispatch is enabled.
func TestRoutingExecutor_Availability_ContainerGatedOnDispatch(t *testing.T) {
	disabled := newTestExecutor(t, Options{Docker: &recordingBackend{}})
	status := disabled.Availability().Container
	assert.False(t, status.Available, "container must require the dispatch opt-in even with a backend present")
	assert.Contains(t, status.Reason, "allow_cloud_dispatch")

	enabled := newTestExecutor(t, Options{CloudDispatchEnabled: true, Docker: &recordingBackend{}})
	assert.True(t, enabled.Availability().Container.Available)
}

func TestRoutingExecutor_Availability_HasLocalTasks(t *testing.T) {
	e := newTestExecutor(t, Options{HasLocalTasks: true})
	assert.True(t, e.Availability().Config.Available)
}

func TestRoutingExecutor_SetRunUpdateCallback_ReceivesUpdates(t *testing.T) {
	e := newTestExecutor(t, Options{})

	called := false
	e.SetRunUpdateCallback(func(r *model.Run) {
		called = true
	})

	// notifyRunUpdated forwards to whichever callback is installed; reach
	// in through the package-internal method to verify the wiring.
	e.notifyRunUpdated(&model.Run{ID: "r1"}, "")
	assert.True(t, called)
}

// TestRoutingExecutor_notifyRunUpdated_PublishesCopy pins the H3 fix: the run
// pointer handed to notifyRunUpdated is still being mutated by the execute
// goroutine (recordRunOutcome → run.End()) while SSE/cloud subscribers marshal
// the event on their own goroutines. notifyRunUpdated must publish a copy, not
// the shared pointer, so a subscriber never races the mutation. We assert the
// published Run is a distinct pointer and that a later mutation of the original
// does not leak into the already-published event.
func TestRoutingExecutor_notifyRunUpdated_PublishesCopy(t *testing.T) {
	eb := events.NewEventBus()
	e := newTestExecutor(t, Options{EventBus: eb})

	var (
		mu        sync.Mutex
		published *model.Run
	)
	unsub := eb.Subscribe(events.EventRunUpdated, func(ev events.Event) {
		re, ok := ev.Data.(events.RunEvent)
		if !ok {
			return
		}
		mu.Lock()
		published = re.Run
		mu.Unlock()
	})
	defer unsub()

	original := &model.Run{ID: "r-copy", Status: model.PhaseRunning}
	e.notifyRunUpdated(original, "")

	mu.Lock()
	got := published
	mu.Unlock()

	require.NotNil(t, got, "run.updated must carry a Run payload")
	assert.NotSame(t, original, got, "notifyRunUpdated must publish a copy, not the shared pointer")
	assert.Equal(t, original.ID, got.ID, "the copy must preserve the run's fields")

	// The execute goroutine keeps mutating the original after publish; that
	// must not be observable through the already-published (copied) event.
	reason := model.ReasonSuccess
	original.End(reason, 0, time.Now())
	assert.Equal(t, model.PhaseRunning, got.Status,
		"post-publish mutation of the original must not leak into the published copy")
}

func TestRoutingExecutor_SetOnProcessStarted_ReceivesCall(t *testing.T) {
	e := newTestExecutor(t, Options{})

	gotID := ""
	e.SetOnProcessStarted(func(runID string, _ func()) {
		gotID = runID
	})

	// Manually invoke the registered hook — startBackend would call it in
	// production, but we cover the registration wiring here.
	require.NotNil(t, e.onProcessStarted)
	e.onProcessStarted("r1", func() {})
	assert.Equal(t, "r1", gotID)
}

func TestRoutingExecutor_Execute_MissingExecutionDefinition(t *testing.T) {
	e := newTestExecutor(t, Options{CloudDispatchEnabled: true})
	// Task with no run and no resolved execution definition → resolveBackend
	// returns the "missing execution definition" error.
	task := &model.Task{Name: "nodef"}
	run := &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}
	result := e.Execute(context.Background(), task, run)
	require.NotNil(t, result.Error)
	assert.Equal(t, -1, result.ExitCode)
	assert.Contains(t, result.Error.Error(), "missing execution definition")
}

func TestRoutingExecutor_Execute_BlockedURLSurfacesStartFailure(t *testing.T) {
	// HTTPBackend.Start rejects private IPs; routing executor must surface that
	// via startBackend's error path with a system log line.
	e := newTestExecutor(t, Options{})
	task := &model.Task{
		Name:         "blocked",
		ExecutionDef: &model.HTTPExecution{Method: "GET", URL: "http://127.0.0.1/admin"},
	}
	run := &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}
	result := e.Execute(context.Background(), task, run)
	require.NotNil(t, result.Error)
	assert.Equal(t, -1, result.ExitCode)
	assert.Contains(t, result.Error.Error(), "failed to start")
}
