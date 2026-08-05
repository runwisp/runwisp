// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
	"github.com/spf13/cobra"
)

// promoteOpts holds the promote command's flag values, read at the RunE boundary
// and passed by value into the logic below.
type promoteOpts struct {
	all    bool // --all: promote every staged task
	reload bool // --reload: reconcile a running daemon afterwards
	dryRun bool // --dry-run: show what would move, write nothing
}

var promoteFlags promoteOpts

var promoteCmd = &cobra.Command{
	Use:   "promote [TASK...]",
	Short: "Move an imported task into your own runwisp.toml",
	Long: `Graduate a staged or cron-sourced task into your own runwisp.toml, where
you own it outright.

` + "`runwisp import --write`" + ` stages jobs in ` + config.StagingIncludeGlob + ` and marks
them "staged" — imported, not yet native. Promoting one moves its block into your
root config: after that a re-import leaves it alone, and it's yours to edit.

A task read live out of a crontab via ` + "`include_cron`" + ` promotes the same way, and
the move is real there too: the block lands in your root config, and the exact
crontab line it came from is deleted in the same transaction — never left behind
for a still-live cron daemon to fire a second time. Promote refuses, writing
nothing, if that line has changed or gone missing since the config was loaded.

The move is textual. The block's comments, its formatting, and any unresolved
# TODO notes the import left behind travel with it byte-for-byte — nothing is
re-generated. Every file involved is written as one transaction, so if the
result wouldn't load, nothing changes. That transaction cannot be made atomic
across the crontab and runwisp.toml themselves — a hard kill between the two
writes is the one gap left, and it leaves the job briefly unscheduled rather
than double-fired.

Nothing about what the daemon runs changes: only which file defines it. Run
` + "`runwisp reload`" + ` (or pass --reload) to clear the provenance marker on a live daemon.`,
	Example: `  runwisp promote backup           # move one task
  runwisp promote backup reindex   # move several
  runwisp promote --all --reload   # move everything, then reconcile
  runwisp promote --all --dry-run  # show what would move`,
	Args:          cobra.ArbitraryArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPromote(cmd, args, flags, promoteFlags)
	},
}

func init() {
	promoteCmd.Flags().BoolVar(&promoteFlags.all, "all", false, "promote every staged task")
	promoteCmd.Flags().BoolVar(&promoteFlags.reload, "reload", false, "reload the running daemon afterwards so the staged marker clears")
	promoteCmd.Flags().BoolVar(&promoteFlags.dryRun, "dry-run", false, "print what would move without writing anything")
}

func runPromote(cmd *cobra.Command, args []string, f Flags, opts promoteOpts) error {
	out := cmd.OutOrStdout()

	layout := configedit.NewLayout(f.CfgFile)
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		// Promote has to know which tasks are staged before it can move anything,
		// and that answer only exists in a config that loads. Refusing here also
		// means a broken config is never blamed on the promotion.
		return &userFacingError{
			title:   fmt.Sprintf("can't promote from %s — it doesn't load", f.CfgFile),
			details: err.Error() + "\n\nFix the config, then re-run. Nothing was written.",
		}
	}

	if err := checkPromoteArgs(args, opts, cfg, layout); err != nil {
		return err
	}

	names, err := configedit.Select(cfg, layout, args, opts.all)
	if err != nil {
		return promoteSelectError(err, f.CfgFile)
	}
	if len(names) == 0 {
		// Only reachable via --all: nothing is staged, so the config is already
		// where the operator wants it. Exit 0 so re-running this in a script is a
		// no-op rather than a failure.
		fmt.Fprintln(out, "Nothing to promote — every task in your config is already native.")
		return nil
	}

	if opts.dryRun {
		return printPromotePlan(out, layout, names, cfg)
	}

	res, err := configedit.Promote(configedit.PromoteRequest{Layout: layout, Names: names, Config: cfg})
	if err != nil {
		return promoteError(err, layout)
	}
	printPromoted(out, res, layout)

	if opts.reload {
		fmt.Fprintln(out)
		return runReload(cmd, f)
	}
	return nil
}

// checkPromoteArgs rejects the two ways the invocation itself doesn't make sense,
// listing what is available so the operator's next command is obvious.
func checkPromoteArgs(args []string, opts promoteOpts, cfg *config.Config, layout configedit.Layout) error {
	if opts.all && len(args) > 0 {
		return &userFacingError{
			title:   "--all promotes everything, so it can't be combined with task names",
			details: "Drop --all to promote just the tasks you named.",
		}
	}
	if !opts.all && len(args) == 0 {
		return &userFacingError{
			title:   "name a task to promote, or pass --all",
			details: promotableHint(configedit.PromotableNames(cfg)),
		}
	}
	return nil
}

// promotableHint lists what is currently promotable, or says plainly that nothing
// is.
func promotableHint(names []string) string {
	if len(names) == 0 {
		return "Nothing is promotable right now — everything in your config is already native."
	}
	return "Promotable tasks:\n  " + strings.Join(names, "\n  ")
}

// promoteSelectError phrases a refused selection. Naming a task that is already
// native is an error rather than a silent skip: the operator asked for something
// specific and deserves to know why it didn't happen.
func promoteSelectError(err error, cfgPath string) error {
	var unknown *configedit.UnknownEntryError
	if errors.As(err, &unknown) {
		return &userFacingError{
			title:   fmt.Sprintf("no task named %q in %s", unknown.Name, cfgPath),
			details: "Run `runwisp list` to see what's configured. Nothing was written.",
		}
	}
	var notStaged *configedit.NotStagedError
	if errors.As(err, &notStaged) {
		if notStaged.File == "" {
			return &userFacingError{
				title: fmt.Sprintf("%q isn't a staged task", notStaged.Name),
				details: "It's generated from a compose project, so it has no block of its own to move. " +
					"Nothing was written.",
			}
		}
		return &userFacingError{
			title: fmt.Sprintf("%q is already native", notStaged.Name),
			details: fmt.Sprintf("It's defined in %s, which you maintain — there's nothing to promote. Nothing was written.",
				notStaged.File),
		}
	}
	return err
}

// promoteError phrases a failed move. Every case here rolled the transaction
// back, so every file touched is exactly as it was.
func promoteError(err error, layout configedit.Layout) error {
	var conflict *configedit.ConflictError
	if errors.As(err, &conflict) {
		return &userFacingError{
			title: "the promoted config wouldn't load — nothing was written",
			details: conflict.Err.Error() +
				"\n\nEvery file was restored. This shouldn't happen for a plain move; " +
				"please report it at https://github.com/runwisp/runwisp/issues.",
		}
	}
	var mismatch *configedit.CronSourceMismatchError
	if errors.As(err, &mismatch) {
		return &userFacingError{
			title:   "can't safely promote — a crontab line changed underneath it",
			details: mismatch.Error() + "\n\nNothing was written. Run `runwisp validate` to see the crontab as it is now, then re-run promote.",
		}
	}
	return configEditError(err, layout)
}

// printPromotePlan is --dry-run: exactly what would move, and where to and
// from, without touching a file. A cron-sourced task has two sides to that
// move — the block landing in root and the line leaving the crontab — and
// both are shown, or the same refusal a real promote would give is surfaced
// here instead.
func printPromotePlan(out io.Writer, layout configedit.Layout, names []string, cfg *config.Config) error {
	_, blocks, err := configedit.PreviewBlocks(layout, names, cfg)
	if err != nil {
		return promoteError(err, layout)
	}

	var cronNames []string
	for _, name := range names {
		if _, ok := cfg.CronBlockTOML(name); ok {
			cronNames = append(cronNames, name)
		}
	}
	removals, err := configedit.PreviewCronRemovals(cronNames, cfg)
	if err != nil {
		return promoteError(err, layout)
	}

	fmt.Fprintf(out, "Would add %s to %s:\n", pluralizeCounts(countBlocks(blocks)), layout.RootPath)
	for _, b := range blocks {
		fmt.Fprintf(out, "\n%s\n", strings.TrimRight(b.Text, "\n"))
	}
	if len(removals) > 0 {
		fmt.Fprintf(out, "\nWould remove %d crontab line(s):\n", len(removals))
		for _, r := range removals {
			fmt.Fprintf(out, "  %s:%d: %s\n", r.File, r.Line, r.Text)
		}
	}
	fmt.Fprintln(out, "\nNothing was written.")
	return nil
}

// printPromoted reports a completed move.
func printPromoted(out io.Writer, res configedit.PromoteResult, layout configedit.Layout) {
	tasks, services := countBlocks(res.Promoted)
	fmt.Fprintf(out, "Promoted %s into %s:\n", pluralizeCounts(tasks, services), layout.RootPath)
	for _, b := range res.Promoted {
		fmt.Fprintf(out, "  %s\n", b.Name)
	}
	if res.StagingRemoved {
		fmt.Fprintf(out, "\n%s held nothing else, so it was removed.\n", config.StagingRelPath())
	}
	if len(res.CronRemovals) > 0 {
		fmt.Fprintf(out, "\nRemoved %d crontab line(s):\n", len(res.CronRemovals))
		for _, r := range res.CronRemovals {
			fmt.Fprintf(out, "  %s:%d\n", r.File, r.Line)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "What the daemon runs did not change — only which file defines it.\n")
	fmt.Fprintf(out, "Run `runwisp reload` to clear the provenance marker on a running daemon.\n")
}

// countBlocks splits promoted blocks into task and service counts for the
// summary line.
func countBlocks(blocks []configedit.Block) (tasks, services int) {
	for _, b := range blocks {
		if b.Table == "services" {
			services++
			continue
		}
		tasks++
	}
	return tasks, services
}

// promoteStagedFooter is the nudge `runwisp list` prints when the config still
// holds tasks RunWisp derived rather than the operator writing them, so the promote
// path is discoverable from the command an operator already runs.
func promoteStagedFooter(cfg *config.Config, cfgPath string) string {
	names := configedit.PromotableNames(cfg)
	if len(names) == 0 {
		return ""
	}
	noun := "task is"
	if len(names) > 1 {
		noun = "tasks are"
	}
	return fmt.Sprintf("%d %s imported or read from a crontab, not yet native — `runwisp promote <name>` moves one into %s.",
		len(names), noun, filepath.Base(cfgPath))
}
