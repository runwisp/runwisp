// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/spf13/cobra"
)

var execFlags struct {
	Daemon     bool
	Standalone bool
}

// execCmd runs a task and streams its output to stdout/stderr. It auto-detects
// whether a daemon owns this data dir: if one is running, the request is
// dispatched over the REST API; otherwise the task runs in-process. The user
// can pin the choice with --daemon or --standalone.
var execCmd = &cobra.Command{
	Use:   "exec <task-name>",
	Short: "Run a task and stream its output",
	Long: `Runs the named task and streams its log lines to stdout/stderr. Exits with
the task's exit code.

If a daemon is running against the same data dir, ` + "`runwisp exec`" + ` dispatches
the run through its REST API and follows the live log stream. With no daemon
running, the task is executed in this CLI process from runwisp.toml.

Use --daemon to require a running daemon (and fail fast if none is up), or
--standalone to require in-process execution (and refuse if a daemon owns the
data dir). Without either flag, the mode is auto-detected.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if execFlags.Daemon && execFlags.Standalone {
			return errors.New("--daemon and --standalone are mutually exclusive")
		}
		exitCode, err := runExec(args[0], flags)
		if err != nil {
			return err
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	},
}

func init() {
	execCmd.Flags().BoolVar(&execFlags.Daemon, "daemon", false, "require a running daemon and dispatch through its API")
	execCmd.Flags().BoolVar(&execFlags.Standalone, "standalone", false, "require no daemon and run the task in-process")
}

func runExec(taskName string, f Flags) (int, error) {
	daemonUp := isDaemonRunning(f)

	if execFlags.Daemon && !daemonUp {
		return 0, fmt.Errorf("--daemon was set but no daemon is running on data dir %q", f.DataDir)
	}
	if execFlags.Standalone && daemonUp {
		return 0, fmt.Errorf("--standalone was set but a daemon is already running on data dir %q", f.DataDir)
	}

	if daemonUp {
		return runExecViaDaemon(taskName, f)
	}
	return runExecStandalone(taskName, f)
}

// isDaemonRunning reports whether a daemon currently owns this data dir.
// Two writers on one SQLite file would corrupt state, so the standalone
// path must defer to the daemon when one is alive.
func isDaemonRunning(f Flags) bool {
	pidPath := datadir.PidFilePath(f.DataDir)
	pid, err := datadir.ReadPidFile(f.DataDir)
	if err != nil {
		return false
	}
	return processAlive(pid, pidPath)
}

// runExecViaDaemon dispatches the run through the running daemon's REST API
// (via its local Unix socket) and follows its SSE log stream until the run
// reaches a terminal state.
func runExecViaDaemon(taskName string, f Flags) (int, error) {
	client := apiclient.NewUnix(localAPISocketPath(f))
	if err := client.HealthCheck(); err != nil {
		return 0, fmt.Errorf("daemon is not reachable at %s (%w) — %s", localAPISocketPath(f), err, daemonNotRunningHint)
	}

	run, err := client.TriggerRun(taskName)
	if err != nil {
		if apiclient.IsHTTPStatus(err, http.StatusNotFound) {
			return 0, unknownTaskError(taskName, daemonTaskNames(client))
		}
		return 0, fmt.Errorf("trigger %q: %w", taskName, err)
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID)

	ctx, cancel := newSignalCancelContext()
	defer cancel()

	// Line numbers are zero-indexed; the server reads from=0 as the default
	// tail window and clamps to anchor 0 on a fresh run, so we see every line.
	ch, err := client.StreamLogLines(ctx, taskName, run.ID, apiclient.StreamLogOpts{FromLine: 0})
	if err != nil {
		return 0, fmt.Errorf("open log stream: %w", err)
	}

	if exitCode, done, err := streamRunLogs(ch, client, taskName, run.ID); done {
		return exitCode, err
	}

	// Stream closed without a Done event (ctx cancelled or transport ended);
	// poll for the terminal state so we can propagate the exit code anyway.
	final, err := client.GetRun(taskName, run.ID)
	if err != nil {
		return 0, fmt.Errorf("fetch final run state: %w", err)
	}
	return exitCodeFromRun(final), nil
}

// newSignalCancelContext returns a context cancelled either by its own cancel
// func or by an interrupt/SIGTERM, so an exec run forwards Ctrl+C to the stream.
func newSignalCancelContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, func() {
		signal.Stop(sig)
		cancel()
	}
}

// streamRunLogs prints each streamed log line to stdout/stderr until the stream
// reports the run is done or errors. The done bool reports whether a terminal
// outcome was reached (so the caller can return exitCode/err); when the channel
// drains without a Done event it returns done=false for the caller to poll.
func streamRunLogs(ch <-chan apiclient.LogStreamMsg, client *apiclient.Client, taskName, runID string) (exitCode int, done bool, err error) {
	for msg := range ch {
		switch msg.Kind {
		case apiclient.LogStreamMsgKindLine:
			if msg.Line.Stream == logutil.StreamStderr {
				fmt.Fprintln(os.Stderr, msg.Line.Text)
			} else {
				fmt.Fprintln(os.Stdout, msg.Line.Text)
			}
		case apiclient.LogStreamMsgKindDone:
			final, getErr := client.GetRun(taskName, runID)
			if getErr != nil {
				return 0, true, fmt.Errorf("fetch final run state: %w", getErr)
			}
			return exitCodeFromRun(final), true, nil
		case apiclient.LogStreamMsgKindErr:
			return 0, true, fmt.Errorf("log stream error: %w", msg.ErrValue)
		}
	}
	return 0, false, nil
}

// daemonTaskNames fetches the daemon's task list for the unknown-task
// suggestion. Best-effort: a failed fetch just means no list in the error.
func daemonTaskNames(client *apiclient.Client) []string {
	tasks, err := client.ListTasks()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(tasks))
	for _, t := range tasks {
		names = append(names, t.Name)
	}
	return names
}

func exitCodeFromRun(run *model.Run) int {
	if run == nil {
		return 0
	}
	if run.EndReason != nil && *run.EndReason != model.ReasonSuccess {
		return run.ExitCode
	}
	return 0
}

// runExecStandalone runs the task in this CLI process. The data dir must not
// already be owned by a daemon (isDaemonRunning's caller has confirmed that).
func runExecStandalone(taskName string, f Flags) (int, error) {
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		return 0, fmt.Errorf("failed to load %s: %w", f.CfgFile, err)
	}

	if err := config.Validate(cfg); err != nil {
		return 0, fmt.Errorf("invalid configuration: %w", err)
	}

	var target *model.Task
	names := make([]string, 0, len(cfg.Tasks))
	for i := range cfg.Tasks {
		names = append(names, cfg.Tasks[i].Name)
		if cfg.Tasks[i].Name == taskName {
			target = &cfg.Tasks[i]
		}
	}
	if target == nil {
		return 0, unknownTaskError(taskName, names)
	}

	eventBus := events.NewEventBus()
	exec := initExecutor(cfg, eventBus, f.LogDir())

	taskManager := runtime.NewTaskManager(exec, eventBus, time.Now)
	defer taskManager.Shutdown()

	taskManager.UpsertTask(target)

	done := make(chan *events.RunEvent, 1)
	unsubLog := eventBus.Subscribe(events.EventLogLine, execLogLineHandler(taskName))
	defer unsubLog()

	termHandler := execRunTerminalHandler(taskName, done)
	unsubComplete := eventBus.Subscribe(events.EventRunCompleted, termHandler)
	defer unsubComplete()
	unsubFailed := eventBus.Subscribe(events.EventRunFailed, termHandler)
	defer unsubFailed()

	run, err := taskManager.TriggerRun(taskName, model.TriggeredByAPI)
	if err != nil {
		return 0, fmt.Errorf("failed to trigger task %q: %w", taskName, err)
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID)

	result := <-done

	if result.Run.EndReason != nil && *result.Run.EndReason != model.ReasonSuccess {
		return result.Run.ExitCode, nil
	}

	slog.Info("Task completed", "name", taskName, "status", result.Run.Status)
	return 0, nil
}

func execLogLineHandler(taskName string) func(events.Event) {
	return func(e events.Event) {
		ll, ok := e.Data.(events.LogLineEvent)
		if !ok || ll.TaskName != taskName {
			return
		}
		switch ll.Stream {
		case logutil.StreamStderr:
			fmt.Fprintln(os.Stderr, ll.Text)
		default:
			fmt.Fprintln(os.Stdout, ll.Text)
		}
	}
}

func execRunTerminalHandler(taskName string, done chan<- *events.RunEvent) func(events.Event) {
	return func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run != nil && re.Run.TaskName == taskName {
			done <- &re
		}
	}
}
