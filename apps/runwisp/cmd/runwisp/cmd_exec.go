// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/spf13/cobra"
	"log/slog"
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
		exitCode, err := runExec(args[0])
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

func runExec(taskName string) (int, error) {
	daemonUp := isDaemonRunning()

	if execFlags.Daemon && !daemonUp {
		return 0, fmt.Errorf("--daemon was set but no daemon is running on data dir %q", flags.DataDir)
	}
	if execFlags.Standalone && daemonUp {
		return 0, fmt.Errorf("--standalone was set but a daemon is already running on data dir %q", flags.DataDir)
	}

	if daemonUp {
		return runExecViaDaemon(taskName)
	}
	return runExecStandalone(taskName)
}

// isDaemonRunning reports whether a daemon currently owns this data dir.
// Two writers on one SQLite file would corrupt state, so the standalone
// path must defer to the daemon when one is alive.
func isDaemonRunning() bool {
	pidPath := datadir.PidFilePath(flags.DataDir)
	pid, err := datadir.ReadPidFile(flags.DataDir)
	if err != nil {
		return false
	}
	return processAlive(pid, pidPath)
}

// runExecViaDaemon dispatches the run through the running daemon's REST API
// and follows its SSE log stream until the run reaches a terminal state.
func runExecViaDaemon(taskName string) (int, error) {
	client, err := newLocalAuthedClient()
	if err != nil {
		return 0, fmt.Errorf("authentication setup failed: %w", err)
	}
	if err := client.HealthCheck(); err != nil {
		return 0, fmt.Errorf("daemon is not reachable at %s: %w", localAPIBaseURL(), err)
	}

	run, err := client.TriggerRun(taskName)
	if err != nil {
		return 0, fmt.Errorf("trigger %q: %w", taskName, err)
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()

	ch, err := client.StreamLogLines(ctx, taskName, run.ID, apiclient.StreamLogOpts{FromLine: 1})
	if err != nil {
		return 0, fmt.Errorf("open log stream: %w", err)
	}

	for msg := range ch {
		switch msg.Kind {
		case apiclient.LogStreamMsgKindLine:
			if msg.Line.Stream == logutil.StreamStderr {
				fmt.Fprintln(os.Stderr, msg.Line.Text)
			} else {
				fmt.Fprintln(os.Stdout, msg.Line.Text)
			}
		case apiclient.LogStreamMsgKindDone:
			final, err := client.GetRun(taskName, run.ID)
			if err != nil {
				return 0, fmt.Errorf("fetch final run state: %w", err)
			}
			return exitCodeFromRun(final), nil
		case apiclient.LogStreamMsgKindErr:
			return 0, fmt.Errorf("log stream error: %w", msg.ErrValue)
		}
	}

	// Stream closed without a Done event (ctx cancelled or transport ended);
	// poll for the terminal state so we can propagate the exit code anyway.
	final, err := client.GetRun(taskName, run.ID)
	if err != nil {
		return 0, fmt.Errorf("fetch final run state: %w", err)
	}
	return exitCodeFromRun(final), nil
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
func runExecStandalone(taskName string) (int, error) {
	cfg, err := config.Load(flags.CfgFile)
	if err != nil {
		return 0, fmt.Errorf("failed to load %s: %w", flags.CfgFile, err)
	}

	if err := config.Validate(cfg); err != nil {
		return 0, fmt.Errorf("invalid configuration: %w", err)
	}

	var target *model.Task
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Name == taskName {
			target = &cfg.Tasks[i]
			break
		}
	}
	if target == nil {
		return 0, fmt.Errorf("task %q not found in %s", taskName, flags.CfgFile)
	}

	eventBus := events.NewEventBus()
	exec := initExecutor(cfg, eventBus)

	taskManager := runtime.NewTaskManager(exec, eventBus)
	defer taskManager.Shutdown()

	taskManager.UpsertTask(target)

	done := make(chan *events.RunEvent, 1)
	unsubLog := eventBus.Subscribe(events.EventLogLine, func(e events.Event) {
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
	})
	defer unsubLog()

	unsubComplete := eventBus.Subscribe(events.EventRunCompleted, func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run != nil && re.Run.TaskName == taskName {
			done <- &re
		}
	})
	defer unsubComplete()

	unsubFailed := eventBus.Subscribe(events.EventRunFailed, func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run != nil && re.Run.TaskName == taskName {
			done <- &re
		}
	})
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
