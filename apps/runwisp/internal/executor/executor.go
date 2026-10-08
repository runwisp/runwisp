// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/procstat"
)

const (
	StreamReadBufferSize = 16 * 1024
	MaxLineBufferSize    = 64 * 1024 // 64KB max cells per row before an oversized-line split
)

type Executor interface {
	Execute(ctx context.Context, task *model.Task, run *model.Run) *ExecuteResult
	Availability() Availability
}

type ExecuteResult struct {
	ExitCode int
	Error    error
	TimedOut bool
	Stopped  bool
	// KillReason is why a policy stopped the run (log_on_full = "kill" →
	// log_overflow, a failing health check → unhealthy); empty when none did.
	KillReason    model.EndReason
	OutputMatched bool // an output line matched a `failures` output pattern
	// PeakMemoryBytes and CPUTimeMs are the run's resource totals; nil when
	// the backend has no measurable process or the value is unknown.
	PeakMemoryBytes *int64
	CPUTimeMs       *int64
}

// EndReason maps the raw process outcome to a terminal reason. Exit 0 is the sole
// success code, unless the output matched a `failures` output pattern; anything
// else that exited is ReasonFailed. Whether a ReasonFailed run counts as a
// *failure* is a separate, per-task decision (Task.IsFailureReason, driven by
// the `failures` config) applied later — not here.
func (r *ExecuteResult) EndReason() model.EndReason {
	switch {
	case r.TimedOut:
		return model.ReasonTimeout
	case r.KillReason != "":
		return r.KillReason
	case r.Stopped:
		return model.ReasonStopped
	case r.ExitCode == 0 && !r.OutputMatched:
		return model.ReasonSuccess
	default:
		return model.ReasonFailed
	}
}

// RoutingExecutor dispatches task execution to the appropriate Backend
// based on the task's execution type, while managing log files and events.
type RoutingExecutor struct {
	logDir           string
	onUpdate         func(*model.Run)
	onProcessStarted func(runID string, forceKill func())
	watcher          RunWatcher
	eventBus         *events.Bus
	backends         map[string]Backend
	availability     Availability
	minFreeDisk      atomic.Int64 // swapped by SetMinFreeDisk on a config reload
	clock            func() time.Time
	sampler          *procstat.Sampler
}

type Options struct {
	LogDir                 string
	EventBus               *events.Bus
	StationDispatchEnabled bool
	HasLocalTasks          bool
	Docker                 Backend // container backend; nil when Docker is unavailable
	Compose                Backend // compose backend; nil when docker compose is unavailable
	MinFreeDisk            int64   // minimum free disk space in bytes; 0 = disabled
	// Clock is the wall-clock source for captured-output timestamps (system
	// lines and the per-line timestamp index). nil defaults to time.Now;
	// the demo seeder injects a backdated clock so historical runs carry
	// their original date.
	Clock func() time.Time
	// Sampler measures the CPU and memory of shell runs while they are alive.
	// nil disables live usage and sampled peaks (rusage CPU time still lands).
	Sampler *procstat.Sampler
}

// New creates a routing executor with available backends.
//
// Availability is the authorization surface for the Station control plane: the
// dispatch path rejects any type whose BackendStatus is unavailable. It does
// not gate local runwisp.toml tasks, which resolve backends directly and always
// have full access.
//
// StationDispatchEnabled is the operator opt-in (daemon.allow_station_dispatch).
// The policy is a whitelist: only config-backed
// dispatch (triggering an existing TOML task) is permitted without it, since
// that's the sole type that doesn't run peer-supplied code or make a
// peer-directed network call. HTTP, shell, container, compose — and any future
// dispatchable type — require the opt-in.
func New(opts Options) *RoutingExecutor {
	backends := make(map[string]Backend)
	avail := Availability{}

	// Backends are registered unconditionally so local TOML tasks can use them;
	// Availability separately governs what the Station peer may dispatch.
	backends["http"] = &HTTPBackend{}
	backends["shell"] = &ShellBackend{}
	if opts.Docker != nil {
		backends["container"] = opts.Docker
	}
	if opts.Compose != nil {
		backends["compose"] = opts.Compose
	}

	// Always dispatchable: config-backed dispatch when local tasks exist.
	if opts.HasLocalTasks {
		avail.Config = BackendStatus{Available: true}
	} else {
		avail.Config = BackendStatus{Available: false, Reason: "no local tasks configured"}
	}

	// HTTP and the code-executing types require the dispatch opt-in.
	if !opts.StationDispatchEnabled {
		const reason = "station dispatch disabled (set [daemon] allow_station_dispatch = true to enable)"
		avail.HTTP = BackendStatus{Available: false, Reason: reason}
		avail.Shell = BackendStatus{Available: false, Reason: reason}
		avail.Container = BackendStatus{Available: false, Reason: reason}
		avail.Compose = BackendStatus{Available: false, Reason: reason}
	} else {
		avail.HTTP = BackendStatus{Available: true}
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
	eventBus := opts.EventBus
	if eventBus == nil {
		eventBus = events.NewEventBus() // the demo seeder runs with no subscribers
	}

	r := &RoutingExecutor{
		logDir:       opts.LogDir,
		eventBus:     eventBus,
		backends:     backends,
		availability: avail,
		clock:        clock,
		sampler:      opts.Sampler,
	}
	r.minFreeDisk.Store(opts.MinFreeDisk)
	return r
}

// SetMinFreeDisk changes [storage] min_free_space for runs started from now on;
// a run already writing keeps the threshold it started with.
func (r *RoutingExecutor) SetMinFreeDisk(n int64) {
	r.minFreeDisk.Store(n)
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

// SetRunWatcher registers the RunWatcher started alongside every run's
// process. Late-binding mirrors SetOnProcessStarted.
func (r *RoutingExecutor) SetRunWatcher(watcher RunWatcher) {
	r.watcher = watcher
}

// Execute resolves the execution backend and runs the task, streaming output.
func (r *RoutingExecutor) Execute(ctx context.Context, task *model.Task, run *model.Run) *ExecuteResult {
	diskErr := r.checkDisk()

	killer := newRunKiller(ctx)
	defer killer.cancel()
	writer, logPath, err := r.prepareLogWriter(task, run, killer)
	if err != nil {
		// No log to put the reason in; the daemon log is all there is.
		if diskErr != nil {
			err = diskErr
		}
		slog.Error("Run failed before it started", "task", task.Name, "run", run.ID, "err", err)
		return &ExecuteResult{ExitCode: -1, Error: err}
	}
	defer func() {
		if err := writer.Close(); err != nil {
			slog.Warn("Failed to close run log writer", "task", task.Name, "run", run.ID, "err", err)
		}
	}()

	r.notifyRunUpdated(run, logPath)

	if diskErr != nil {
		r.systemLine(writer, task, run, diskErr.Error())
		return &ExecuteResult{ExitCode: -1, Error: diskErr}
	}

	backend, execDef, errResult := r.resolveBackend(task, run, writer)
	if errResult != nil {
		return errResult
	}

	proc, errResult := r.startBackend(killer.ctx, backend, task, run, execDef, writer)
	if errResult != nil {
		return errResult
	}
	if r.onProcessStarted != nil {
		r.onProcessStarted(run.ID, proc.ForceKill)
	}
	stopWatcher := r.startWatcher(task, run, writer, killer)
	stopSampling := r.track(task, run, proc)

	matcher := &outputMatcher{res: task.Failures.OutputRegexps()}
	r.streamProcessOutput(proc, writer, task, run, matcher)

	exitCode, waitErr := proc.Wait()
	if proc.Cleanup != nil {
		proc.Cleanup()
	}
	// Seal before stopping the watcher: from here the process has exited on
	// its own terms, and no late kill may relabel how it ended.
	killReason := killer.seal()
	stopWatcher()

	result := classifyExecuteResult(killer.ctx, killReason, exitCode, waitErr)
	if msg := abnormalExitMessage(result.Error); msg != "" {
		r.systemLine(writer, task, run, msg)
	}
	result.OutputMatched = matcher.pattern() != ""
	peak, sampled := stopSampling()
	var ru *syscall.Rusage
	if proc.Rusage != nil {
		ru = proc.Rusage()
	}
	result.PeakMemoryBytes, result.CPUTimeMs = runUsage(ru, peak, sampled)
	return result
}

// track starts sampling the run's process group when the backend exposes one.
// The returned stop reports the sampled peak and is a no-op otherwise.
func (r *RoutingExecutor) track(task *model.Task, run *model.Run, proc *Process) (stop func() (int64, bool)) {
	if r.sampler == nil || proc.Pid <= 0 {
		return func() (int64, bool) { return 0, false }
	}
	return r.sampler.Track(task.Name, run.ID, proc.Pid)
}

// abnormalExitMessage describes a wait error that the exit code alone does not
// explain: death by signal (exit -1, "signal: killed"), or a backend failing to
// wait at all (a lost Docker connection). A plain non-zero exit is already
// shown as the exit code, so it returns "".
func abnormalExitMessage(err error) string {
	if err == nil {
		return ""
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return "Could not wait for the run to finish: " + err.Error()
	}
	if exitErr.ExitCode() >= 0 {
		return ""
	}
	return "Process terminated abnormally: " + err.Error()
}

// outputMatcher checks output lines against a task's `failures` output
// patterns. One is shared by a run's (or probe's) stdout and stderr goroutines.
type outputMatcher struct {
	res     []*regexp.Regexp
	matched atomic.Pointer[regexp.Regexp] // first pattern that matched
}

// match reports the pattern text matched, but only for the first match: one
// is enough to decide the outcome, so later lines are not checked.
func (m *outputMatcher) match(text string) (*regexp.Regexp, bool) {
	if len(m.res) == 0 || m.matched.Load() != nil {
		return nil, false
	}
	for _, re := range m.res {
		if re.MatchString(text) && m.matched.CompareAndSwap(nil, re) {
			return re, true
		}
	}
	return nil, false
}

// pattern is the source of the pattern that matched, or "" when none did.
func (m *outputMatcher) pattern() string {
	if re := m.matched.Load(); re != nil {
		return re.String()
	}
	return ""
}

// notifyRunUpdated reports a run state change to the onUpdate callback (when
// wired) and the event bus. logPath is the freshly resolved on-disk log file; the executor carries it on the event envelope (not the
// Run row, which is never persisted with a log path) so station and notify
// subscribers can locate the captured output.
func (r *RoutingExecutor) notifyRunUpdated(run *model.Run, logPath string) {
	if r.onUpdate != nil {
		r.onUpdate(run)
	}
	// Copy before publishing: the execute goroutine keeps mutating this
	// *Run (recordRunOutcome → run.End()) while SSE/station subscribers
	// marshal the event on their own goroutines. Sharing the pointer is a
	// data race, matching every other publish site.
	r.eventBus.Publish(events.EventRunUpdated, events.RunEvent{
		Run:     run.Copy(),
		LogPath: logPath,
	})
}

// resolveBackend picks the execution backend matching the task's resolved
// definition and returns an *ExecuteResult only when resolution fails. The
// error path writes a synthetic system log line so operators see the failure
// inline with the rest of the run.
func (r *RoutingExecutor) resolveBackend(task *model.Task, run *model.Run, writer *LogWriter) (Backend, model.ExecutionDef, *ExecuteResult) {
	execDef := task.ResolvedExecutionDef()
	if execDef == nil {
		return nil, nil, &ExecuteResult{ExitCode: -1, Error: errors.New("missing execution definition")}
	}
	backend, ok := r.backends[execDef.ExecType()]
	if !ok {
		errMsg := fmt.Sprintf("unsupported execution type: %s", execDef.ExecType())
		r.systemLine(writer, task, run, errMsg)
		return nil, nil, &ExecuteResult{ExitCode: -1, Error: errors.New(errMsg)}
	}
	return backend, execDef, nil
}

func (r *RoutingExecutor) startBackend(ctx context.Context, backend Backend, task *model.Task, run *model.Run, execDef model.ExecutionDef, writer *LogWriter) (*Process, *ExecuteResult) {
	proc, err := backend.Start(ctx, task, run, execDef)
	if err != nil {
		errMsg := fmt.Sprintf("failed to start %s execution: %v", execDef.ExecType(), err)
		r.systemLine(writer, task, run, errMsg)
		return nil, &ExecuteResult{ExitCode: -1, Error: errors.New(errMsg)}
	}
	return proc, nil
}

// streamProcessOutput tees both standard streams into the run's log writer
// and blocks until each goroutine finishes. Panics inside the streamer are
// logged so one misbehaving backend cannot abort Execute mid-flight.
func (r *RoutingExecutor) streamProcessOutput(proc *Process, writer *LogWriter, task *model.Task, run *model.Run, matcher *outputMatcher) {
	var wg sync.WaitGroup
	r.streamOne(&wg, proc.Stdout, writer, task, run, matcher, logutil.StreamStdout)
	r.streamOne(&wg, proc.Stderr, writer, task, run, matcher, logutil.StreamStderr)
	wg.Wait()
}

func (r *RoutingExecutor) streamOne(wg *sync.WaitGroup, reader io.ReadCloser, writer *LogWriter, task *model.Task, run *model.Run, matcher *outputMatcher, stream string) {
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
		r.streamToFile(reader, writer, task, run, matcher, stream)
	}()
}

// classifyExecuteResult translates wait state + cancellation cause into the
// terminal ExecuteResult. killReason is the policy kill recorded by the run's
// runKiller, if any. Wait errors are only surfaced when no context-driven
// cancellation explains them, since the OS error is expected after a
// timeout / stop / policy kill.
func classifyExecuteResult(ctx context.Context, killReason model.EndReason, exitCode int, waitErr error) *ExecuteResult {
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	killed := !timedOut && killReason != ""
	stopped := !timedOut && !killed && errors.Is(ctx.Err(), context.Canceled)

	var resultErr error
	if waitErr != nil && !timedOut && !stopped && !killed {
		resultErr = waitErr
	}

	result := &ExecuteResult{
		ExitCode: exitCode,
		Error:    resultErr,
		TimedOut: timedOut,
		Stopped:  stopped,
	}
	if killed {
		result.KillReason = killReason
	}
	return result
}

func (r *RoutingExecutor) prepareLogWriter(task *model.Task, run *model.Run, killer *runKiller) (*LogWriter, string, error) {
	logPath := logutil.ResolveRunLogPath(r.logDir, task.Name, run.ID, run.CreatedAt)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return nil, "", fmt.Errorf("create task log dir: %w", err)
	}

	writer, err := NewLogWriter(LogWriterOpts{
		LogPath:     logPath,
		MaxSize:     task.LogMaxSize,
		Overflow:    task.LogOnFull,
		Kill:        func() { killer.kill(model.ReasonLogOverflow) },
		MinFreeDisk: r.minFreeDisk.Load(),
		LogDir:      r.logDir,
		Now:         r.clock,
		OnDiskPressure: func(free, minFree int64, killed bool) {
			r.eventBus.Publish(events.EventLogDiskPressure, events.LogDiskPressureEvent{
				TaskName:     task.Name,
				RunID:        run.ID,
				FreeBytes:    free,
				MinFreeBytes: minFree,
				KilledTask:   killed,
			})
		},
	})
	if err != nil {
		return nil, "", err
	}
	return writer, logPath, nil
}

func (r *RoutingExecutor) checkDisk() error {
	if err := os.MkdirAll(r.logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	if minFree := r.minFreeDisk.Load(); minFree > 0 {
		if free := freeDiskSpace(r.logDir); free >= 0 && free < minFree {
			return fmt.Errorf(
				"insufficient disk space: %s free, minimum %s required",
				config.FormatByteSize(free), config.FormatByteSize(minFree))
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
	redact *secretRedactor,
	publish func(text string, lineNum int64, continued bool, frameCount int),
) {
	// Scrub secret values once, before the text reaches either sink (disk file
	// and the frame sidecar below, the event bus in publish). Redacting here
	// covers SSE, the REST log endpoints, and the station push in one place.
	texts := make([]string, len(lines))
	for i, line := range lines {
		texts[i] = redact.text(line.text)
	}

	ns, anchor := writeCommittedLines(writer, stream, texts)

	frameCount := 0
	if len(frames) > 0 && anchor >= 0 {
		if err := writer.WriteFrameHistory(anchor, redact.frames(frames)); err != nil {
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
		publish(texts[i], ns[i], line.continued, fc)
	}
}

// writeCommittedLines writes each text to the log file, returning per-line
// file offsets (-1 for a line that failed to write) and the anchor offset:
// the first successfully written line's offset, used to key frame history.
func writeCommittedLines(writer *LogWriter, stream string, texts []string) ([]int64, int64) {
	ns := make([]int64, len(texts))
	anchor := int64(-1)
	for i, text := range texts {
		n, err := writer.WriteLineEvent(text, stream)
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
	return ns, anchor
}

func (r *RoutingExecutor) streamToFile(reader io.Reader, writer *LogWriter, task *model.Task, run *model.Run, matcher *outputMatcher, stream string) {
	executionID := model.OrDefault(run.ExecutionID, "")
	nowMs := func() int64 { return r.clock().UnixMilli() }

	// publishCommitted sees each successfully written line's redacted text, so
	// output patterns match exactly what the operator sees in the log.
	publishCommitted := func(text string, lineNum int64, continued bool, frameCount int) {
		// Note the match inline, so the operator sees why an exit-0 run turned red.
		if re, ok := matcher.match(text); ok {
			writer.WriteLineEvent(fmt.Sprintf("Output matched failures pattern %q; run will be marked failed", re.String()), logutil.StreamSystem)
		}
		r.publishLine(task, run, stream, text, lineNum, continued, frameCount)
	}

	publishRegion := func(epoch int, rows []string) {
		r.eventBus.Publish(events.EventLogRegion, events.LogRegionEvent{
			TaskName:    task.Name,
			RunID:       run.ID,
			ExecutionID: executionID,
			Timestamp:   nowMs(),
			Stream:      stream,
			Epoch:       epoch,
			Rows:        rows,
		})
	}

	redact := newSecretRedactor(task.Secrets)

	renderer := NewTerminalRenderer(
		func(lines []committedLine, frames [][]string) {
			commitGroup(writer, stream, lines, frames, redact, publishCommitted)
		},
		func(epoch int, rows []string) {
			publishRegion(epoch, redact.rows(rows))
		},
		nowMs,
	)

	buf := make([]byte, StreamReadBufferSize)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			renderer.Write(buf[:n])
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				// A genuine I/O error (disk full, fd closed out from under us,
				// permission revoked mid-run, ...) is not the same as the
				// process's output ending normally. Surface it instead of
				// silently treating it like EOF, both to the daemon log and
				// inline in the run's own log so the operator can tell the
				// capture was cut short by a real error.
				slog.Warn("Process output stream ended with a non-EOF error", "stream", stream, "task", task.Name, "err", err)
				r.systemLine(writer, task, run, fmt.Sprintf("Output capture stopped: %v", err))
			}
			break
		}
	}

	renderer.Close()
}

// systemLine writes a daemon-authored SYSTEM line into the run's log and
// publishes it like captured output, so the live view shows it too, not only
// the stored log.
func (r *RoutingExecutor) systemLine(writer *LogWriter, task *model.Task, run *model.Run, msg string) {
	lineNum, err := writer.WriteLineEvent(msg, logutil.StreamSystem)
	if err != nil || lineNum < 0 {
		return
	}
	r.publishLine(task, run, logutil.StreamSystem, msg, lineNum, false, 0)
}

// publishLine announces one line already written to the run's log on the
// event bus (SSE, station push).
func (r *RoutingExecutor) publishLine(task *model.Task, run *model.Run, stream, text string, lineNum int64, continued bool, frameCount int) {
	r.eventBus.Publish(events.EventLogLine, events.LogLineEvent{
		TaskName:    task.Name,
		RunID:       run.ID,
		ExecutionID: model.OrDefault(run.ExecutionID, ""),
		LineNum:     lineNum,
		Timestamp:   r.clock().UnixMilli(),
		Stream:      stream,
		Text:        text,
		Continued:   continued,
		FrameCount:  frameCount,
	})
}
