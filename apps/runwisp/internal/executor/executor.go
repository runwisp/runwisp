// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

const (
	StreamReadBufferSize  = 16 * 1024 // 16KB buffer for reading from stdout/stderr
	InitialLineBufferSize = 4 * 1024  // 4KB initial line buffer
	MaxLineBufferSize     = 64 * 1024 // 64KB max before flushing partial line
)

// Executor defines the interface for running tasks.
type Executor interface {
	Execute(ctx context.Context, task *model.Task, run *model.Run) *ExecuteResult
	Availability() Availability
}

// ExecuteResult summarizes a completed execution.
type ExecuteResult struct {
	ExitCode       int
	Error          error
	TimedOut       bool
	Stopped        bool
	KilledByPolicy bool // log_on_full = "kill_task" tripped — recorded as failed, not stopped
}

// RoutingExecutor dispatches task execution to the appropriate Backend
// based on the task's execution type, while managing log files and events.
type RoutingExecutor struct {
	logDir           string
	onUpdate         func(*model.Run)
	onProcessStarted func(runID string, forceKill func())
	eventBus         events.EventBus
	backends         map[string]Backend
	availability     Availability
	diskChecker      *DiskChecker
	streamer         *StreamManager
}

// Options configures the RoutingExecutor at startup.
type Options struct {
	LogDir            string
	EventBus          events.EventBus
	CloudShellEnabled bool
	HasLocalTasks     bool
	Docker            Backend          // container backend; nil when Docker is unavailable
	MinFreeDisk       int64            // minimum free disk space in bytes; 0 = disabled
	OnRunUpdate       func(*model.Run) // called when run state changes (e.g. LogPath set)
}

// New creates a routing executor with available backends.
// CloudShellEnabled only controls what is reported to the cloud control plane;
// the cloud dispatch path uses Availability to reject disallowed types.
// Local tasks from runwisp.toml always have shell access.
func New(opts Options) Executor {
	backends := make(map[string]Backend)
	avail := Availability{
		HTTP: BackendStatus{Available: true},
	}

	backends["http"] = &HTTPBackend{}
	backends["shell"] = &ShellBackend{}

	if opts.CloudShellEnabled {
		avail.Shell = BackendStatus{Available: true}
	} else {
		avail.Shell = BackendStatus{Available: false, Reason: "cloud shell dispatch disabled (set cloudShellTasks: true to enable)"}
	}

	if opts.Docker != nil {
		backends["container"] = opts.Docker
		avail.Container = BackendStatus{Available: true}
	} else {
		avail.Container = BackendStatus{Available: false, Reason: "docker daemon unreachable"}
	}

	if opts.HasLocalTasks {
		avail.Config = BackendStatus{Available: true}
	} else {
		avail.Config = BackendStatus{Available: false, Reason: "no local tasks configured"}
	}

	return &RoutingExecutor{
		logDir:       opts.LogDir,
		onUpdate:     opts.OnRunUpdate,
		eventBus:     opts.EventBus,
		backends:     backends,
		availability: avail,
		diskChecker:  NewDiskChecker(opts.LogDir, opts.MinFreeDisk),
		streamer:     NewStreamManager(opts.EventBus),
	}
}

// Availability returns the backend availability status.
func (r *RoutingExecutor) Availability() Availability {
	return r.availability
}

// SetRunUpdateCallback registers a hook to persist run updates.
// This is a concrete method (not on the Executor interface) for late binding.
func (r *RoutingExecutor) SetRunUpdateCallback(callback func(*model.Run)) {
	r.onUpdate = callback
}

// SetOnProcessStarted registers a hook fired immediately after a backend
// successfully starts a process. The hook receives the run ID and the
// process's ForceKill closure (when present), letting the manager wire a
// daemon-shutdown SIGKILL path. Late-binding mirrors SetRunUpdateCallback.
func (r *RoutingExecutor) SetOnProcessStarted(callback func(runID string, forceKill func())) {
	r.onProcessStarted = callback
}

// Execute resolves the execution backend and runs the task, streaming output.
func (r *RoutingExecutor) Execute(ctx context.Context, task *model.Task, run *model.Run) *ExecuteResult {
	if err := r.diskChecker.Check(); err != nil {
		return &ExecuteResult{ExitCode: -1, Error: err}
	}

	writer, logPath, cancelCtx, cancelFunc, err := r.prepareLogWriter(ctx, task, run)
	if err != nil {
		return &ExecuteResult{ExitCode: -1, Error: err}
	}
	defer writer.Close()
	defer cancelFunc()

	run.LogPath = logPath
	if r.onUpdate != nil {
		r.onUpdate(run)
	}

	if r.eventBus != nil {
		r.eventBus.Publish(events.EventRunUpdated, events.RunEvent{
			Run: run,
		})
	}

	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return &ExecuteResult{ExitCode: -1, Error: errors.New("missing execution definition")}
	}
	backend, ok := r.backends[execDef.ExecType()]
	if !ok {
		errMsg := fmt.Sprintf("unsupported execution type: %s", execDef.ExecType())
		writer.WriteLineEvent(errMsg, logutil.StreamSystem)
		return &ExecuteResult{ExitCode: -1, Error: errors.New(errMsg)}
	}

	proc, err := backend.Start(cancelCtx, task, execDef)
	if err != nil {
		errMsg := fmt.Sprintf("failed to start %s execution: %v", execDef.ExecType(), err)
		writer.WriteLineEvent(errMsg, logutil.StreamSystem)
		return &ExecuteResult{ExitCode: -1, Error: errors.New(errMsg)}
	}
	if r.onProcessStarted != nil {
		r.onProcessStarted(run.ID, proc.ForceKill)
	}

	var wg sync.WaitGroup
	streamOutput := func(reader io.ReadCloser, stream string) {
		if reader == nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					slog.Error("Recovered from panic in stream", "stream", stream, "task", task.Name, "err", rec)
				}
			}()
			r.streamer.StreamToFile(reader, writer, task, run, stream)
		}()
	}

	streamOutput(proc.Stdout, logutil.StreamStdout)
	streamOutput(proc.Stderr, logutil.StreamStderr)
	wg.Wait()

	exitCode, waitErr := proc.Wait()
	if proc.Cleanup != nil {
		proc.Cleanup()
	}

	timedOut := errors.Is(cancelCtx.Err(), context.DeadlineExceeded)
	killedByPolicy := writer.KilledByPolicy()
	stopped := !timedOut && !killedByPolicy && errors.Is(cancelCtx.Err(), context.Canceled)

	// Propagate unexpected wait errors; ignore them when context cancellation
	// is the cause (timed out / stopped / policy-killed) since the OS error
	// is expected then.
	var resultErr error
	if waitErr != nil && !timedOut && !stopped && !killedByPolicy {
		resultErr = waitErr
	}

	return &ExecuteResult{
		ExitCode:       exitCode,
		Error:          resultErr,
		TimedOut:       timedOut,
		Stopped:        stopped,
		KilledByPolicy: killedByPolicy,
	}
}

func (r *RoutingExecutor) prepareLogWriter(ctx context.Context, task *model.Task, run *model.Run) (*LogWriter, string, context.Context, context.CancelFunc, error) {
	logPath := logutil.ResolveRunLogPath(r.logDir, task.Name, run.ID, run.CreatedAt)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return nil, "", nil, nil, fmt.Errorf("create task log dir: %w", err)
	}
	idxPath := logPath + ".idx"
	tidxPath := logPath + ".tidx"

	cancelCtx, cancelFunc := context.WithCancel(ctx)

	bus := r.eventBus
	taskName := task.Name
	runID := run.ID
	writer, err := NewLogWriter(LogWriterOpts{
		LogPath:     logPath,
		IdxPath:     idxPath,
		TidxPath:    tidxPath,
		MaxSize:     task.LogMaxSize,
		Overflow:    task.LogOnFull,
		CancelFunc:  cancelFunc,
		MinFreeDisk: r.diskChecker.minFreeDisk,
		LogDir:      r.logDir,
		OnDiskPressure: func(free, min int64, killed bool) {
			if bus == nil {
				return
			}
			bus.Publish(events.EventLogDiskPressure, events.LogDiskPressureEvent{
				TaskName:     taskName,
				RunID:        runID,
				FreeBytes:    free,
				MinFreeBytes: min,
				KilledTask:   killed,
			})
		},
	})
	if err != nil {
		cancelFunc()
		return nil, "", nil, nil, err
	}
	return writer, logPath, cancelCtx, cancelFunc, nil
}
