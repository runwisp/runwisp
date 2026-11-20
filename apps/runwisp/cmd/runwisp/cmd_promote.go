// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/configedit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronprobe"
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
	Long: `Move a staged task (from ` + "`runwisp import --write`" + `) or one read from a
crontab into your own runwisp.toml, where you edit it like any other task.
A crontab job's line is commented out in the same step, so cron stops running
it. Nothing is written if the result wouldn't load.

Run ` + "`runwisp reload`" + ` afterwards, or pass --reload.
Details: https://docs.runwisp.com/reference/cli/#runwisp-promote`,
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
	if errors.Is(err, fs.ErrNotExist) && systemDaemonElsewhere(f) {
		return configLoadError(f, err)
	}
	if err != nil {
		// Promote has to know which tasks are staged before it can move anything,
		// and that answer only exists in a config that loads. Refusing here also
		// means a broken config is never blamed on the promotion.
		return &userFacingError{
			title:   fmt.Sprintf("can't promote from %s — it doesn't load", f.CfgFile),
			details: err.Error() + "\n\nFix the config, then re-run. Nothing was written.",
		}
	}

	if err := checkPromoteArgs(args, opts, cfg); err != nil {
		return err
	}

	names, err := configedit.Select(cfg, args, opts.all)
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
	reload := ""
	if !opts.reload {
		reload = reloadCommand(f)
	}
	cronLive := len(res.CronCommentOuts) > 0 && cronprobe.Probe().Live
	printPromoted(out, res, layout, cronLive, reload)

	if opts.reload {
		fmt.Fprintln(out)
		return reloadRunningDaemon(cmd.Context(), f, cmd.OutOrStdout())
	}
	return nil
}

// checkPromoteArgs rejects the two ways the invocation itself doesn't make sense,
// listing what is available so the operator's next command is obvious.
func checkPromoteArgs(args []string, opts promoteOpts, cfg *config.Config) error {
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
// move — the block landing in root and the line being commented out in the
// crontab — and both are shown, or the same refusal a real promote would give
// is surfaced here instead.
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
	commentOuts, _, err := configedit.PlanCronCommentOuts(cronNames, cfg, layout.RootPath)
	if err != nil {
		return promoteError(err, layout)
	}

	fmt.Fprintf(out, "Would add %s to %s:\n", pluralizeCounts(countBlocks(blocks)), layout.RootPath)
	for _, b := range blocks {
		fmt.Fprintf(out, "\n%s\n", strings.TrimRight(b.Text, "\n"))
	}
	if len(commentOuts) > 0 {
		fmt.Fprintf(out, "\nWould comment out %d crontab line(s):\n", len(commentOuts))
		for _, c := range commentOuts {
			fmt.Fprintf(out, "  %s:%d: %s\n", c.File, c.Line, c.Text)
		}
	}
	fmt.Fprintln(out, "\nNothing was written.")
	return nil
}

// reloadCommand is how this operator reloads the daemon: under sudo when they
// ran this under sudo, or when RunWisp runs as the system service.
func reloadCommand(f Flags) string {
	if (os.Geteuid() == 0 && os.Getenv("SUDO_USER") != "") || systemDaemonElsewhere(f) {
		return sudoSystemRunwisp("reload")
	}
	return "runwisp reload"
}

// printPromoted reports a completed move. cronLive says whether a cron daemon
// still runs, so only the case that applies is described. reload is the
// command that applies the move, "" when --reload is about to.
func printPromoted(out io.Writer, res configedit.PromoteResult, layout configedit.Layout, cronLive bool, reload string) {
	tasks, services := countBlocks(res.Promoted)
	fmt.Fprintf(out, "Promoted %s into %s:\n", pluralizeCounts(tasks, services), layout.RootPath)
	for _, b := range res.Promoted {
		fmt.Fprintf(out, "  %s\n", b.Name)
	}
	if res.StagingRemoved {
		fmt.Fprintf(out, "\n%s held nothing else, so it was removed.\n", config.StagingRelPath())
	}
	if len(res.CronCommentOuts) > 0 {
		fmt.Fprintf(out, "\nCommented out %d crontab line(s):\n", len(res.CronCommentOuts))
		for _, c := range res.CronCommentOuts {
			fmt.Fprintf(out, "  %s:%d\n", c.File, c.Line)
		}
	}

	fmt.Fprintln(out)
	switch {
	case len(res.CronCommentOuts) == 0:
		fmt.Fprintln(out, "What the daemon runs did not change, only which file defines it.")
	case cronLive:
		fmt.Fprintln(out, "The crontab line is commented out, so cron no longer runs this job. RunWisp held")
		fmt.Fprintln(out, "it while cron did; after a reload, RunWisp runs it.")
	default:
		fmt.Fprintln(out, "The crontab line is commented out. RunWisp already ran this job, so only the file")
		fmt.Fprintln(out, "that defines it changed.")
	}
	if reload != "" {
		fmt.Fprintf(out, "Run `%s` so a running daemon picks it up.\n", reload)
	}
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
