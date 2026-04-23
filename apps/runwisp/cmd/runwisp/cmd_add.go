// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/spf13/cobra"
	"log/slog"
)

var addCmd = &cobra.Command{
	Use:   "add [name] [cron] [command]",
	Short: "Add a new task to the configuration",
	Long: `Interactively add a new task to runwisp.yaml.

Provide all arguments for quick add:
  runwisp add backup-db "0 2 * * *" "pg_dump mydb"

Provide just the name to be prompted for the rest:
  runwisp add backup-db

Or run fully interactively:
  runwisp add

Use empty cron for an API-only task (no schedule):
  runwisp add backup-db "" "pg_dump mydb"`,
	Args: func(cmd *cobra.Command, args []string) error {
		switch len(args) {
		case 0, 1, 3:
			return nil
		default:
			return fmt.Errorf("expected 0, 1, or 3 arguments, got %d\n\nUsage:\n  runwisp add                            (interactive)\n  runwisp add <name>                     (prompted)\n  runwisp add <name> <cron> <command>    (quick add)", len(args))
		}
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAdd(args)
	},
}

func runAdd(args []string) error {
	draft := newDraft()

	switch len(args) {
	case 3:
		draft.Name = args[0]
		draft.Cron = args[1]
		draft.Command = args[2]
	case 1:
		draft.Name = args[0]
	}

	if draft.Cron != "" {
		if err := validateCronExpr(draft.Cron); err != nil {
			return fmt.Errorf("invalid cron expression %q: %w", draft.Cron, err)
		}
	}

	scanner := bufio.NewScanner(os.Stdin)

	if draft.Name == "" || draft.Command == "" {
		if !promptRequiredFields(scanner, draft) {
			return nil
		}
	}

	draft.Name = model.SanitizeTaskName(draft.Name)

	doc, created, err := config.EnsureConfigFile(flags.CfgFile)
	if err != nil {
		return err
	}
	if created {
		slog.Info("Created configuration file", "path", flags.CfgFile)
	}

	existingNames := config.TaskNamesFromDocument(doc)
	if isNameTaken(draft.Name, existingNames, "") {
		return fmt.Errorf("task %q already exists in %s", draft.Name, flags.CfgFile)
	}

	ctx := promptContext{
		confirmLabel:  "Confirm and add",
		existingNames: existingNames,
	}
	return runTaskEditor(scanner, doc, draft, ctx, "")
}
