// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/log"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/spf13/cobra"
)

var triggerCmd = &cobra.Command{
	Use:     "trigger <task-name>",
	Aliases: []string{"exec"},
	Short:   "Trigger a single task and stream its output",
	Long:    `Runs the specified task immediately from the CLI, streaming its log output to stdout. Exits with the task's exit code.`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		exitCode, err := runTrigger(args[0])
		if err != nil {
			return err
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	},
}

func runTrigger(taskName string) (int, error) {
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

	// Subscribe to log lines and completion before triggering
	done := make(chan *events.RunEvent, 1)
	unsubLog := eventBus.Subscribe(events.EventLogLine, func(e events.Event) {
		if ll, ok := e.Data.(events.LogLineEvent); ok && ll.TaskName == taskName {
			fmt.Fprintln(os.Stdout, ll.Line)
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

	log.Info("Task triggered", "name", taskName, "run", run.ID)

	result := <-done

	if result.Run.EndReason != nil && *result.Run.EndReason != model.ReasonSuccess {
		return result.Run.ExitCode, nil
	}

	log.Info("Task completed", "name", taskName, "status", result.Run.Status)
	return 0, nil
}
