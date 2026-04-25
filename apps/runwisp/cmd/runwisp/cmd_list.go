// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Show configured tasks and their schedules",
	Long:  `Reads the configuration file and displays all configured tasks as a formatted table.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runList()
	},
}

func runList() error {
	cfg, err := config.Load(flags.CfgFile)
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", flags.CfgFile, err)
	}

	if len(cfg.Tasks) == 0 {
		fmt.Println("No tasks configured.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tSCHEDULE\tCONCURRENCY\tPOLICY\tAPI\tDESCRIPTION")
	fmt.Fprintln(w, "----\t--------\t-----------\t------\t---\t-----------")

	for _, task := range cfg.Tasks {
		schedule := task.Cron
		if schedule == "" {
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
			task.Parallelism,
			task.OnOverlap,
			api,
			desc,
		)
	}

	return w.Flush()
}
