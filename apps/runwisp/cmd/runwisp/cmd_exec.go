// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/spf13/cobra"
	"log/slog"
)

// execCmd runs a task in-process from the CLI. It opens the SQLite store
// directly, so it MUST refuse when a daemon is already attached to the same
// data dir — two writers on one SQLite file is data corruption, not a
// recoverable error. Operators who want to fire a task against a running
// daemon should use `runwisp run-task` instead.
var execCmd = &cobra.Command{
	Use:   "exec <task-name>",
	Short: "Run a task in-process and stream its output",
	Long: `Loads runwisp.toml, runs the named task in this CLI process, and streams
log lines to stdout/stderr. Exits with the task's exit code.

This command opens the local SQLite store directly. If a daemon is already
running against the same data dir, ` + "`runwisp exec`" + ` refuses with an error —
use ` + "`runwisp run-task`" + ` to dispatch via the running daemon's REST API.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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

func runExec(taskName string) (int, error) {
	if err := refuseIfDaemonRunning(taskName); err != nil {
		return 0, err
	}

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

// refuseIfDaemonRunning errors out when an existing daemon owns the data
// directory. Two writers on the same SQLite file silently corrupt state —
// the operator must either stop the daemon or use `runwisp run-task` to
// dispatch through the running daemon's API.
func refuseIfDaemonRunning(taskName string) error {
	pidPath := datadir.PidFilePath(flags.DataDir)
	pid, err := datadir.ReadPidFile(flags.DataDir)
	if err != nil {
		return nil
	}
	if !processAlive(pid, pidPath) {
		return nil
	}
	return fmt.Errorf(
		"a daemon is already running on data dir %q (pid %d); refusing to open SQLite as a second writer. "+
			"Run `runwisp run-task %s` to dispatch through the running daemon, or stop the daemon first",
		flags.DataDir, pid, taskName,
	)
}
