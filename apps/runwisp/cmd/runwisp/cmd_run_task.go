// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/spf13/cobra"
)

// runTaskCmd dispatches a single run against a running daemon via the REST
// API. The complementary command is `runwisp exec`, which runs in-process
// and refuses when a daemon is up.
var runTaskCmd = &cobra.Command{
	Use:   "run-task <task-name>",
	Short: "Trigger a run against a running daemon via the REST API",
	Long: `Asks the running daemon to start a new run for the named task. Requires
the daemon to be reachable at the configured host:port and the operator's
password to be available (via RUNWISP_PASSWORD or data/password).

For in-process execution without a daemon, use ` + "`runwisp exec`" + ` instead.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRunTask(args[0])
	},
}

func runRunTask(taskName string) error {
	password, _, err := datadir.ResolvePassword(flags.DataDir)
	if err != nil {
		return fmt.Errorf("resolve password: %w", err)
	}

	client := apiclient.New(localAPIBaseURL(), password)
	if err := client.HealthCheck(); err != nil {
		return fmt.Errorf("daemon is not reachable at %s: %w (start it with `runwisp daemon`)", localAPIBaseURL(), err)
	}
	if err := client.Authenticate(); err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return passwordMismatchError(flags.Port)
		}
		return fmt.Errorf("authenticate: %w", err)
	}

	run, err := client.TriggerRun(taskName)
	if err != nil {
		return fmt.Errorf("trigger %q: %w", taskName, err)
	}
	fmt.Printf("triggered run %s for task %q\n", run.ID, taskName)
	return nil
}
