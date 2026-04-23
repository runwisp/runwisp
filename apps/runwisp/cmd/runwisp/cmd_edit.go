// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

var editCmd = &cobra.Command{
	Use:   "edit [name]",
	Short: "Edit an existing task in the configuration",
	Long: `Interactively edit an existing task in runwisp.yaml.

Provide the task name:
  runwisp edit backup-db

Or select from a list:
  runwisp edit`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runEdit(args)
	},
}

func runEdit(args []string) error {
	rawCfg, err := config.LoadRaw(flags.CfgFile)
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", flags.CfgFile, err)
	}
	if len(rawCfg.Tasks) == 0 {
		return fmt.Errorf("no tasks found in %s", flags.CfgFile)
	}

	scanner := bufio.NewScanner(os.Stdin)

	var taskIdx int
	if len(args) == 1 {
		found := false
		for i, t := range rawCfg.Tasks {
			if t.Name == args[0] {
				taskIdx = i
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("task %q not found in %s", args[0], flags.CfgFile)
		}
	} else {
		idx, ok := promptSelectTask(scanner, rawCfg.Tasks)
		if !ok {
			return nil
		}
		taskIdx = idx
	}

	originalName := rawCfg.Tasks[taskIdx].Name
	draft := newDraftFromTask(rawCfg.Tasks[taskIdx])

	doc, err := config.ReadDocument(flags.CfgFile)
	if err != nil {
		return err
	}

	ctx := promptContext{
		confirmLabel:  "Save changes",
		existingNames: config.TaskNamesFromDocument(doc),
		originalName:  originalName,
	}
	return runTaskEditor(scanner, doc, draft, ctx, originalName)
}
