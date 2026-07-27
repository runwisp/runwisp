// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/spf13/cobra"
)

var listJSON bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Show configured tasks and their schedules",
	Long:  `Reads the configuration file and displays all configured tasks as a formatted table.`,
	Example: `  runwisp list
  runwisp list --json   # tasks + schedules as JSON`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runList(cmd.OutOrStdout(), flags, listJSON)
	},
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "emit a machine-readable JSON document to stdout instead of the table")
}

func runList(out io.Writer, f Flags, asJSON bool) error {
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		if asJSON {
			// Emit an error document so an agent learns of the failure from the
			// JSON, not from stderr text; the returned error still drives exit 1.
			// Tasks is an explicit empty slice (not nil) so the error document
			// still emits "tasks": [], matching the success path and the sibling
			// status/validate commands rather than a lone "tasks": null.
			if werr := writeJSON(out, listJSONDoc{SchemaVersion: jsonSchemaVersion, Tasks: []listTaskJSON{}, Error: err.Error()}); werr != nil {
				return werr
			}
		}
		return fmt.Errorf("failed to load %s: %w", f.CfgFile, err)
	}

	if asJSON {
		doc := listJSONDoc{SchemaVersion: jsonSchemaVersion, Tasks: make([]listTaskJSON, 0, len(cfg.Tasks))}
		for _, task := range cfg.Tasks {
			doc.Tasks = append(doc.Tasks, newListTaskJSON(task))
		}
		return writeJSON(out, doc)
	}

	return renderTaskTable(out, cfg.Tasks)
}

// renderTaskTable writes the human-readable task listing (the default,
// non-JSON view) as an aligned table.
func renderTaskTable(out io.Writer, tasks []model.Task) error {
	if len(tasks) == 0 {
		fmt.Fprintln(out, "No tasks configured.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSCHEDULE\tCONCURRENCY\tPOLICY\tAPI\tDESCRIPTION")
	fmt.Fprintln(w, "----\t--------\t-----------\t------\t---\t-----------")

	for _, task := range tasks {
		var schedule string
		switch {
		case task.Kind.IsService():
			schedule = fmt.Sprintf("(service x%d)", task.Instances)
		case task.Cron != "":
			schedule = task.Cron
		default:
			schedule = "(manual)"
		}

		api := "no"
		if task.APITrigger {
			api = "yes"
		}

		desc := task.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
			task.Name,
			schedule,
			task.MaxConcurrent,
			task.OnOverlap,
			api,
			desc,
		)
	}

	return w.Flush()
}
