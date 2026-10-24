// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/spf13/cobra"
)

var listJSON bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Show configured tasks and their schedules",
	Long: `Reads the configuration file and displays all configured tasks as a formatted
table. When a daemon is running on this data dir, each service also shows its
live state (RUNNING, DEGRADED, STOPPED or FATAL).

With --url (or RUNWISP_URL), the tasks come from a remote daemon instead of the
local runwisp.toml.`,
	Example: `  runwisp list
  runwisp list --json   # tasks + schedules as JSON
  runwisp list --url https://rw.example.com`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runList(cmd.Context(), cmd.OutOrStdout(), flags, controlRemote, listJSON)
	},
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "emit a machine-readable JSON document to stdout instead of the table")
	addRemoteFlags(listCmd)
}

func runList(ctx context.Context, out io.Writer, f Flags, rf remoteFlags, asJSON bool) error {
	if url, _ := rf.resolve(); url != "" {
		_, _, tasks, err := connectAndListTasks(ctx, f, rf)
		if err != nil {
			return listFailed(out, asJSON, err)
		}
		return writeTaskList(out, tasks, asJSON)
	}

	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		return listFailed(out, asJSON, configLoadError(f, err))
	}
	live := liveServices(ctx, f)
	tasks := make([]model.TaskResponse, len(cfg.Tasks))
	for i, task := range cfg.Tasks {
		tasks[i] = model.TaskResponse{Task: task, Service: live[task.Name]}
	}
	if err := writeTaskList(out, tasks, asJSON); err != nil || asJSON {
		return err
	}
	// The staged tasks are already marked in --json; the table gets a footer
	// instead of a column, both to keep its shape stable for anything parsing it
	// and because the useful part is the command, not a per-row flag.
	if footer := promoteStagedFooter(cfg, f.CfgFile); footer != "" {
		fmt.Fprintf(out, "\n%s\n", footer)
	}
	return nil
}

// listFailed emits the --json error document, so an agent learns of the
// failure from the JSON and not from stderr text, then returns err to drive
// exit 1. Tasks is an explicit empty slice (not nil) so the error document
// still emits "tasks": [], matching the success path and the sibling
// status/validate commands rather than a lone "tasks": null.
func listFailed(out io.Writer, asJSON bool, err error) error {
	if asJSON {
		if werr := writeJSON(out, listJSONDoc{SchemaVersion: jsonSchemaVersion, Tasks: []listTaskJSON{}, Error: err.Error()}); werr != nil {
			return werr
		}
	}
	return err
}

// writeTaskList writes tasks as the table, or as the --json document.
func writeTaskList(out io.Writer, tasks []model.TaskResponse, asJSON bool) error {
	if !asJSON {
		return renderTaskTable(out, tasks)
	}
	doc := listJSONDoc{SchemaVersion: jsonSchemaVersion, Tasks: make([]listTaskJSON, 0, len(tasks))}
	for _, task := range tasks {
		doc.Tasks = append(doc.Tasks, newListTaskJSON(task))
	}
	return writeJSON(out, doc)
}

// liveStateTimeout bounds the live-state lookup, so a wedged daemon never
// holds up a listing the config alone can answer.
const liveStateTimeout = 2 * time.Second

// liveServices asks the local daemon, when one answers, for each service's
// supervisor state, keyed by name. nil when no daemon is running or it does
// not answer in time: list then shows the config alone.
func liveServices(ctx context.Context, f Flags) map[string]*model.ServiceStatus {
	ctx, cancel := context.WithTimeout(ctx, liveStateTimeout)
	defer cancel()
	tasks, err := apiclient.NewUnix(localAPISocketPath(f)).ListTasks(ctx)
	if err != nil {
		return nil
	}
	live := make(map[string]*model.ServiceStatus, len(tasks))
	for _, t := range tasks {
		live[t.Name] = t.Service
	}
	return live
}

// renderTaskTable writes the human-readable task listing (the default,
// non-JSON view) as an aligned table. A service whose live state is known
// shows it after the instance count.
func renderTaskTable(out io.Writer, tasks []model.TaskResponse) error {
	if len(tasks) == 0 {
		fmt.Fprintln(out, "No tasks configured.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSCHEDULE\tCONCURRENCY\tPOLICY\tTRIGGER\tDESCRIPTION")
	fmt.Fprintln(w, "----\t--------\t-----------\t------\t-------\t-----------")

	for _, task := range tasks {
		var schedule string
		switch {
		case task.Kind.IsService():
			schedule = fmt.Sprintf("(service x%d)", task.Instances)
			if !task.ManuallyControllable() {
				schedule += " [locked]"
			}
			if task.Service != nil {
				schedule += " " + strings.ToUpper(task.Service.State)
			}
		case task.Cron != "":
			schedule = task.Cron
		default:
			schedule = "(manual)"
		}

		// TRIGGER is specifically about manual run-triggering, a task-only
		// concept: a service's manual_trigger locks stop/restart/start
		// instead, called out via "[locked]" in SCHEDULE above.
		trigger := "no"
		policy := string(task.OnOverlap)
		if task.Kind.IsService() {
			trigger = "-"
			policy = "-" // on_overlap doesn't apply; concurrency is `instances`
		} else if task.Triggerable() {
			trigger = "yes"
		}

		desc := task.Description
		if r := []rune(desc); len(r) > 50 {
			desc = string(r[:47]) + "..."
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
			task.Name,
			schedule,
			task.MaxConcurrent,
			policy,
			trigger,
			desc,
		)
	}

	return w.Flush()
}
