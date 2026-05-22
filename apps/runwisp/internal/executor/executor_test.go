// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudShellEnabled: true, HasLocalTasks: true})
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
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudShellEnabled: true, HasLocalTasks: true})

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
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudShellEnabled: true, HasLocalTasks: true})

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
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudShellEnabled: true, HasLocalTasks: true})
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
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudShellEnabled: true, HasLocalTasks: true})

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
		LogDir:            tmpDir,
		EventBus:          eb,
		CloudShellEnabled: true,
		HasLocalTasks:     true,
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
	exec := New(Options{LogDir: tmpFile.Name(), EventBus: eb, CloudShellEnabled: true, HasLocalTasks: true})

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
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudShellEnabled: true, HasLocalTasks: true})

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
	exec := New(Options{LogDir: tmpDir, EventBus: eb, CloudShellEnabled: true, HasLocalTasks: true})

	task := &model.Task{
		Name: "fail-start",
		Run:  "/path/to/non/existent/command",
	}
	run := &model.Run{
		ID:     ulid.Make().String(),
		Status: model.PhaseRunning,
	}

	result := exec.Execute(context.Background(), task, run)
	assert.Equal(t, 127, result.ExitCode) // Shell returns 127 for command not found
}
