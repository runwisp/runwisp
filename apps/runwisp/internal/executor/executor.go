// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

const (
	StreamReadBufferSize = 16 * 1024 // 16KB buffer for reading from stdout/stderr
	MaxLineBufferSize    = 64 * 1024 // 64KB max cells per row before an oversized-line split
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
	// SuccessExitCodes lists the exit codes treated as success. Empty/nil
	// preserves the default contract that only 0 succeeds.
	SuccessExitCodes []int
}

func (r *ExecuteResult) EndReason() model.EndReason {
	switch {
	case r.TimedOut:
		return model.ReasonTimeout
	case r.KilledByPolicy:
		return model.ReasonLogOverflow
	case r.Stopped:
		return model.ReasonStopped
	case isSuccessExitCode(r.ExitCode, r.SuccessExitCodes):
		return model.ReasonSuccess
	default:
		return model.ReasonFailed
	}
}

// isSuccessExitCode reports whether code counts as success given the configured
// success set. An empty/nil set means "only 0 succeeds" — the default.
func isSuccessExitCode(code int, success []int) bool {
	if len(success) == 0 {
		return code == 0
	}
	for _, c := range success {
		if c == code {
			return true
		}
	}
	return false
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
	minFreeDisk      int64
	clock            func() time.Time
}

type Options struct {
	LogDir               string
	EventBus             events.EventBus
	CloudDispatchEnabled bool
	HasLocalTasks        bool
	Docker               Backend // container backend; nil when Docker is unavailable
	Compose              Backend // compose backend; nil when docker compose is unavailable
	MinFreeDisk          int64   // minimum free disk space in bytes; 0 = disabled
	// Clock is the wall-clock source for captured-output timestamps (system
	// lines and the per-line timestamp index). nil defaults to time.Now;
	// the demo seeder injects a backdated clock so historical runs carry
	// their original date.
	Clock func() time.Time
}

// New creates a routing executor with available backends.
//
// Availability is the authorization surface for the cloud control plane: the
// dispatch path rejects any type whose BackendStatus is unavailable. It does
// not gate local runwisp.toml tasks, which resolve backends directly and always
// have full access.
//
// CloudDispatchEnabled is the operator opt-in (daemon.allow_cloud_dispatch). The
// policy is a whitelist: only HTTP and config-backed dispatch (triggering an
// existing TOML task) are permitted without it, since they don't run
// peer-supplied local code. Shell, container, compose — and any future
// code-executing type — require the opt-in.
func New(opts Options) Executor {
	backends := make(map[string]Backend)
	avail := Availability{}

	// Backends are registered unconditionally so local TOML tasks can use them;
	// Availability separately governs what the cloud peer may dispatch.
	backends["http"] = &HTTPBackend{}
	backends["shell"] = &ShellBackend{}
	if opts.Docker != nil {
		backends["container"] = opts.Docker
	}
	if opts.Compose != nil {
		backends["compose"] = opts.Compose
	}

	// Always dispatchable: HTTP, and config-backed dispatch when local tasks exist.
	avail.HTTP = BackendStatus{Available: true}
	if opts.HasLocalTasks {
		avail.Config = BackendStatus{Available: true}
	} else {
		avail.Config = BackendStatus{Available: false, Reason: "no local tasks configured"}
	}

	// Code-executing types require the dispatch opt-in.
	if !opts.CloudDispatchEnabled {
		const reason = "cloud dispatch disabled (set [daemon] allow_cloud_dispatch = true to enable)"
		avail.Shell = BackendStatus{Available: false, Reason: reason}
		avail.Container = BackendStatus{Available: false, Reason: reason}
		avail.Compose = BackendStatus{Available: false, Reason: reason}
	} else {
		avail.Shell = BackendStatus{Available: true}
		if opts.Docker != nil {
			avail.Container = BackendStatus{Available: true}
		} else {
			avail.Container = BackendStatus{Available: false, Reason: "docker daemon unreachable"}
		}
		if opts.Compose != nil {
			avail.Compose = BackendStatus{Available: true}
		} else {
			avail.Compose = BackendStatus{Available: false, Reason: "docker compose CLI unavailable"}
		}
	}

	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	return &RoutingExecutor{
		logDir:       opts.LogDir,
		eventBus:     opts.EventBus,
		backends:     backends,
		availability: avail,
		minFreeDisk:  opts.MinFreeDisk,
		clock:        clock,
	}
}

// now returns the executor's wall-clock instant. The constructor always sets
// clock (defaulting to time.Now), so callers can rely on it being non-nil.
func (r *RoutingExecutor) now() time.Time {
	return r.clock()
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
	if err := r.checkDisk(); err != nil {
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

	return classifyExecuteResult(cancelCtx, writer, exitCode, waitErr, task.ExitCodes)
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
		// Copy before publishing: the execute goroutine keeps mutating this
		// *Run (recordRunOutcome → run.End()) while SSE/cloud subscribers
		// marshal the event on their own goroutines. Sharing the pointer is a
		// data race, matching every other publish site.
		r.eventBus.Publish(events.EventRunUpdated, events.RunEvent{
			Run:     run.Copy(),
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
		r.streamToFile(reader, writer, task, run, stream)
	}()
}

// classifyExecuteResult translates wait state + cancellation cause into the
// terminal ExecuteResult. Wait errors are only surfaced when no context-driven
// cancellation explains them, since the OS error is expected after a
// timeout / stop / log-disk kill.
func classifyExecuteResult(cancelCtx context.Context, writer *LogWriter, exitCode int, waitErr error, successCodes []int) *ExecuteResult {
	timedOut := errors.Is(cancelCtx.Err(), context.DeadlineExceeded)
	killedByPolicy := writer.KilledByPolicy()
	stopped := !timedOut && !killedByPolicy && errors.Is(cancelCtx.Err(), context.Canceled)

	var resultErr error
	if waitErr != nil && !timedOut && !stopped && !killedByPolicy {
		resultErr = waitErr
	}

	return &ExecuteResult{
		ExitCode:         exitCode,
		Error:            resultErr,
		TimedOut:         timedOut,
		Stopped:          stopped,
		KilledByPolicy:   killedByPolicy,
		SuccessExitCodes: successCodes,
	}
}

func (r *RoutingExecutor) prepareLogWriter(ctx context.Context, task *model.Task, run *model.Run) (*LogWriter, string, context.Context, context.CancelFunc, error) {
	logPath := logutil.ResolveRunLogPath(r.logDir, task.Name, run.ID, run.CreatedAt)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return nil, "", nil, nil, fmt.Errorf("create task log dir: %w", err)
	}
	cancelCtx, cancelFunc := context.WithCancel(ctx)

	bus := r.eventBus
	taskName := task.Name
	runID := run.ID
	writer, err := NewLogWriter(LogWriterOpts{
		LogPath:     logPath,
		MaxSize:     task.LogMaxSize,
		Overflow:    task.LogOnFull,
		CancelFunc:  cancelFunc,
		MinFreeDisk: r.minFreeDisk,
		LogDir:      r.logDir,
		Now:         r.now,
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

func (r *RoutingExecutor) checkDisk() error {
	if err := os.MkdirAll(r.logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	if r.minFreeDisk > 0 {
		if free := freeDiskSpace(r.logDir); free >= 0 && free < r.minFreeDisk {
			return fmt.Errorf(
				"insufficient disk space: %s free, minimum %s required",
				config.FormatByteSize(free), config.FormatByteSize(r.minFreeDisk))
		}
	}
	return nil
}

// commitGroup persists one finalized commit group (one line for a forward
// commit, K lines for a multi-line redraw) and publishes a LogLineEvent per
// line. Frame history is keyed to the group's first committed line — the
// clickable anchor — and only that line's published event carries FrameCount.
func commitGroup(
	writer *LogWriter,
	stream string,
	lines []committedLine,
	frames [][]string,
	publish func(text string, lineNum int64, continued bool, frameCount int),
) {
	ns := make([]int64, len(lines))
	anchor := int64(-1)
	for i, line := range lines {
		n, err := writer.WriteLineEvent(line.text, stream)
		if err != nil {
			slog.Warn("Failed to write log line to file", "stream", stream, "err", err)
			ns[i] = -1
			continue
		}
		ns[i] = n
		if anchor < 0 {
			anchor = n
		}
	}

	frameCount := 0
	if len(frames) > 0 && anchor >= 0 {
		if err := writer.WriteFrameHistory(anchor, frames); err != nil {
			slog.Warn("Failed to write frame history", "stream", stream, "err", err)
		} else {
			frameCount = len(frames)
		}
	}

	for i, line := range lines {
		if ns[i] < 0 {
			continue
		}
		fc := 0
		if ns[i] == anchor {
			fc = frameCount
		}
		publish(line.text, ns[i], line.continued, fc)
	}
}

func (r *RoutingExecutor) streamToFile(reader io.Reader, writer *LogWriter, task *model.Task, run *model.Run, stream string) {
	externalExecutionID := ""
	if run.ExternalExecutionID != nil {
		externalExecutionID = *run.ExternalExecutionID
	}

	nowMs := func() int64 { return r.now().UnixMilli() }

	publishCommitted := func(text string, lineNum int64, continued bool, frameCount int) {
		if r.eventBus == nil {
			return
		}
		r.eventBus.Publish(events.EventLogLine, events.LogLineEvent{
			TaskName:            task.Name,
			RunID:               run.ID,
			ExternalExecutionID: externalExecutionID,
			LineNum:             lineNum,
			Timestamp:           nowMs(),
			Stream:              stream,
			Text:                text,
			Continued:           continued,
			FrameCount:          frameCount,
		})
	}

	publishRegion := func(epoch int, rows []string) {
		if r.eventBus == nil {
			return
		}
		r.eventBus.Publish(events.EventLogRegion, events.LogRegionEvent{
			TaskName:            task.Name,
			RunID:               run.ID,
			ExternalExecutionID: externalExecutionID,
			Timestamp:           nowMs(),
			Stream:              stream,
			Epoch:               epoch,
			Rows:                rows,
		})
	}

	renderer := NewTerminalRenderer(
		func(lines []committedLine, frames [][]string) {
			commitGroup(writer, stream, lines, frames, publishCommitted)
		},
		publishRegion,
		nowMs,
	)

	buf := make([]byte, StreamReadBufferSize)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			renderer.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	renderer.Close()
}
