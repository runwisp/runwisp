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
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

const (
	StreamReadBufferSize  = 16 * 1024 // 16KB buffer for reading from stdout/stderr
	InitialLineBufferSize = 4 * 1024  // 4KB initial line buffer
	MaxLineBufferSize     = 64 * 1024 // 64KB max before flushing partial line
)

type Executor interface {
	Execute(ctx context.Context, task *model.Task, run *model.Run) *ExecuteResult
	Availability() Availability
}

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

type Options struct {
	LogDir            string
	EventBus          events.EventBus
	CloudShellEnabled bool
	HasLocalTasks     bool
	Docker            Backend          // container backend; nil when Docker is unavailable
	Compose           Backend          // compose backend; nil when docker compose is unavailable
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

	if opts.Compose != nil {
		backends["compose"] = opts.Compose
		avail.Compose = BackendStatus{Available: true}
	} else {
		avail.Compose = BackendStatus{Available: false, Reason: "docker compose CLI unavailable"}
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

	r.notifyRunUpdated(run, logPath)

	backend, execDef, errResult := r.resolveBackend(task, writer)
	if errResult != nil {
		return errResult
	}

	proc, errResult := r.startBackend(cancelCtx, backend, task, run, execDef, writer)
	if errResult != nil {
		return errResult
	}
	if r.onProcessStarted != nil {
		r.onProcessStarted(run.ID, proc.ForceKill)
	}

	r.streamProcessOutput(proc, writer, task, run)

	exitCode, waitErr := proc.Wait()
	if proc.Cleanup != nil {
		proc.Cleanup()
	}

	return classifyExecuteResult(cancelCtx, writer, exitCode, waitErr)
}

// notifyRunUpdated fans the post-log-prep run state out to the persistence
// callback and event bus when each is wired. logPath is the freshly resolved
// on-disk log file; the executor carries it on the event envelope (not the
// Run row, which is never persisted with a log path) so cloud and notify
// subscribers can locate the captured output.
func (r *RoutingExecutor) notifyRunUpdated(run *model.Run, logPath string) {
	if r.onUpdate != nil {
		r.onUpdate(run)
	}
	if r.eventBus != nil {
		r.eventBus.Publish(events.EventRunUpdated, events.RunEvent{
			Run:     run,
			LogPath: logPath,
		})
	}
}

// resolveBackend picks the execution backend matching the task's resolved
// definition and returns an *ExecuteResult only when resolution fails. The
// error path writes a synthetic system log line so operators see the failure
// inline with the rest of the run.
func (r *RoutingExecutor) resolveBackend(task *model.Task, writer *LogWriter) (Backend, model.ExecutionDef, *ExecuteResult) {
	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return nil, nil, &ExecuteResult{ExitCode: -1, Error: errors.New("missing execution definition")}
	}
	backend, ok := r.backends[execDef.ExecType()]
	if !ok {
		errMsg := fmt.Sprintf("unsupported execution type: %s", execDef.ExecType())
		writer.WriteLineEvent(errMsg, logutil.StreamSystem)
		return nil, nil, &ExecuteResult{ExitCode: -1, Error: errors.New(errMsg)}
	}
	return backend, execDef, nil
}

func (r *RoutingExecutor) startBackend(ctx context.Context, backend Backend, task *model.Task, run *model.Run, execDef model.ExecutionDef, writer *LogWriter) (*Process, *ExecuteResult) {
	proc, err := backend.Start(ctx, task, run, execDef)
	if err != nil {
		errMsg := fmt.Sprintf("failed to start %s execution: %v", execDef.ExecType(), err)
		writer.WriteLineEvent(errMsg, logutil.StreamSystem)
		return nil, &ExecuteResult{ExitCode: -1, Error: errors.New(errMsg)}
	}
	return proc, nil
}

// streamProcessOutput tees both standard streams into the run's log writer
// and blocks until each goroutine finishes. Panics inside the streamer are
// logged so one misbehaving backend cannot abort Execute mid-flight.
func (r *RoutingExecutor) streamProcessOutput(proc *Process, writer *LogWriter, task *model.Task, run *model.Run) {
	var wg sync.WaitGroup
	r.streamOne(&wg, proc.Stdout, writer, task, run, logutil.StreamStdout)
	r.streamOne(&wg, proc.Stderr, writer, task, run, logutil.StreamStderr)
	wg.Wait()
}

func (r *RoutingExecutor) streamOne(wg *sync.WaitGroup, reader io.ReadCloser, writer *LogWriter, task *model.Task, run *model.Run, stream string) {
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

// classifyExecuteResult translates wait state + cancellation cause into the
// terminal ExecuteResult. Wait errors are only surfaced when no context-driven
// cancellation explains them, since the OS error is expected after a
// timeout / stop / log-disk kill.
func classifyExecuteResult(cancelCtx context.Context, writer *LogWriter, exitCode int, waitErr error) *ExecuteResult {
	timedOut := errors.Is(cancelCtx.Err(), context.DeadlineExceeded)
	killedByPolicy := writer.KilledByPolicy()
	stopped := !timedOut && !killedByPolicy && errors.Is(cancelCtx.Err(), context.Canceled)

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
		Now:         time.Now,
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
