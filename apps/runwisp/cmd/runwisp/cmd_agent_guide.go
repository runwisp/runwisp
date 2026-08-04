// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

var agentGuideCmd = &cobra.Command{
	Use:   "agent-guide",
	Short: "Print a paste-ready snippet telling an AI agent how to drive RunWisp",
	Long: `Prints a short Markdown block you can paste into your project's AGENTS.md,
CLAUDE.md, or README so an AI coding agent working in this repo knows RunWisp is
here and how to configure, validate, and run it — the config schema, the tight
validate loop, and the JSON-emitting commands.

The snippet is written to stdout, so you can append it directly:

  runwisp agent-guide >> AGENTS.md`,
	Example: "  runwisp agent-guide >> AGENTS.md",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), agentGuideSnippet)
		return err
	},
}

// agentGuideSnippet is the paste-ready block. It is deliberately compact and
// command-first: it tells a downstream agent the invariants (TOML is the source
// of truth; API is read-only + trigger) and the machine-readable surfaces, then
// points at the dense reference for the full schema.
const agentGuideSnippet = `## RunWisp

This project uses [RunWisp](https://docs.runwisp.com) to schedule tasks and
supervise long-running services. It is a single Go binary; all tasks are defined
in ` + "`runwisp.toml`" + `.

- ` + "`runwisp.toml`" + ` is the **single source of truth** for tasks. The REST API, Web
  UI, and TUI can read state and trigger/stop/restart runs, but never create or
  edit definitions. Edit the TOML, then ` + "`runwisp reload`" + ` (live) or
  ` + "`runwisp restart`" + ` to apply.
- Two unit kinds: ` + "`[tasks.<name>]`" + ` run-to-exit (cron via ` + "`cron = \"...\"`" + `, or
  manual) and ` + "`[services.<name>]`" + ` long-running (auto-restart). ` + "`run = \"...\"`" + `
  is a shell command.
- **Validate after every edit:** ` + "`runwisp validate --json`" + ` — on failure it
  exits non-zero and each error carries ` + "`key`/`line`/`column`" + `.
- **Config schema:** ` + "`runwisp schema`" + ` prints the JSON Schema (also at
  ` + config.SchemaURL + `). Files RunWisp scaffolds start with a ` + "`#:schema`" + ` line, so
  editors validate and autocomplete them.
- **Run a task and read the outcome:** ` + "`runwisp exec <task> --json`" + ` prints
  ` + "`{run_id, status, exit_code, duration_ms, failed, ...}`" + ` to stdout (log lines
  go to stderr).
- **Inspect:** ` + "`runwisp status --json`" + ` (daemon + per-task snapshot),
  ` + "`runwisp list --json`" + ` (configured tasks).
- **Migrating a box that already runs cron:** on Linux+systemd,
  ` + "`sudo runwisp takeover`" + ` retires cron and hands its jobs to RunWisp in one
  step (` + "`--dry-run`" + ` to preview, ` + "`-y`" + ` for scripts). Prefer it over
  ` + "`runwisp import cron`" + `, which copies jobs and will double-fire until the
  crontab is removed by hand.
- Full reference for agents: https://docs.runwisp.com/agents/reference.md
`
