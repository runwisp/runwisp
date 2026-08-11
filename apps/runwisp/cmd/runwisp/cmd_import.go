// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
	"github.com/runwisp/runwisp/internal/importer"
	"github.com/spf13/cobra"
)

// importOpts holds the import subcommands' flag values. Following the Flags
// seam, these are read at the RunE boundary and passed by value into the logic
// helpers below.
type importOpts struct {
	output string // -o/--output: write TOML to this path
	write  bool   // --write: write to the --config path
	force  bool   // overwrite an existing file without prompting
	system bool   // cron: treat input as a system crontab (user column)
	quiet  bool   // suppress the stderr summary
	dryRun bool   // --dry-run: print the summary, write nothing
}

var importFlags importOpts

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Convert a crontab or supervisord config into runwisp.toml",
	Long: `Convert an existing crontab or supervisord configuration into an
annotated runwisp.toml.

The generated TOML is printed to stdout by default so you can review it (and
pipe it). Use -o/--output to save it, or --write to save it to the --config
path. Anything that can't map cleanly onto a RunWisp setting becomes an inline
# TODO comment and a note in the summary, so nothing is silently dropped.

Add --dry-run to a saving import to see the summary and every file it would
touch, without touching any of them.`,
	SilenceErrors: true,
	SilenceUsage:  true,
}

var importCronCmd = &cobra.Command{
	Use:   "cron [FILE]",
	Short: "Convert a crontab into runwisp.toml",
	Long: `Convert a crontab into runwisp.toml.

Pipe a crontab in, or pass a file — both are first-class:

  crontab -l | runwisp import cron
  runwisp import cron /etc/crontab -o runwisp.toml

System crontabs (/etc/crontab, /etc/cron.d/*) carry a user column between the
schedule and the command. RunWisp detects them automatically — from the path,
or from the "# m h dom mon dow user command" header — and maps that column to a
per-task user. Force it on or off with --system / --system=false.`,
	Args:          cobra.MaximumNArgs(1),
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := resolveImportSource(args, os.Stdin, "crontab")
		if err != nil {
			return err
		}
		cronOpts := resolveCronOptions(source, importFlags.system, cmd.Flags().Changed("system"))
		return runImportCron(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, source, cronOpts, flags, importFlags)
	},
}

var importSupervisordCmd = &cobra.Command{
	Use:   "supervisord [FILE...]",
	Short: "Convert a supervisord config into runwisp.toml",
	Long: `Convert a supervisord configuration into runwisp.toml. Each [program]
becomes a RunWisp service.

Pipe a config in, or pass one or more files (any [include] directives are
followed relative to each file):

  runwisp import supervisord /etc/supervisor/supervisord.conf
  cat /etc/supervisor/supervisord.conf | runwisp import supervisord
  runwisp import supervisord /etc/supervisor/conf.d/*.conf -o runwisp.toml`,
	Args:          cobra.ArbitraryArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runImportSupervisord(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, args, flags, importFlags)
	},
}

func init() {
	for _, c := range []*cobra.Command{importCronCmd, importSupervisordCmd} {
		c.Flags().StringVarP(&importFlags.output, "output", "o", "", "write the generated TOML to this file instead of stdout")
		c.Flags().BoolVar(&importFlags.write, "write", false, "write to the --config path (default runwisp.toml)")
		c.Flags().BoolVar(&importFlags.force, "force", false, "overwrite the target file without prompting")
		c.Flags().BoolVar(&importFlags.quiet, "quiet", false, "suppress the summary on stderr")
		c.Flags().BoolVar(&importFlags.dryRun, "dry-run", false, "print the summary without writing anything")
	}
	importCronCmd.Flags().BoolVar(&importFlags.system, "system", false, "force system-crontab parsing (user column); --system=false forces per-user. Auto-detected when unset")

	importCmd.AddCommand(importCronCmd)
	importCmd.AddCommand(importSupervisordCmd)
}

func runImportCron(stdout, stderr io.Writer, stdin *os.File, source string, cronOpts importer.CronOptions, f Flags, opts importOpts) error {
	if err := checkImportFlags(opts); err != nil {
		return err
	}
	cronOpts.Existing = ownedEntries(f, opts)

	r, closeFn, err := openImportSource(source, stdin)
	if err != nil {
		return err
	}
	defer closeFn()

	res, err := importer.ParseCrontab(r, cronOpts)
	if err != nil {
		return &userFacingError{title: "failed to read crontab", details: err.Error()}
	}
	return emitImport(stdout, stderr, stdin, res, sourceCrontab, f, opts)
}

func runImportSupervisord(stdout, stderr io.Writer, stdin *os.File, sources []string, f Flags, opts importOpts) error {
	if err := checkImportFlags(opts); err != nil {
		return err
	}
	svOpts := importer.SupervisordOptions{Existing: ownedEntries(f, opts)}
	var res *importer.Result
	var err error
	switch {
	case len(sources) == 0:
		// Read from stdin or guide the operator.
		if isatty.IsTerminal(stdin.Fd()) {
			return &userFacingError{
				title:   "no supervisord config given",
				details: "Pass a config file, or pipe one in — e.g. `cat supervisord.conf | runwisp import supervisord`.",
			}
		}
		res, err = importer.ParseSupervisordReader(stdin, svOpts)
	case len(sources) == 1 && sources[0] == "-":
		res, err = importer.ParseSupervisordReader(stdin, svOpts)
	default:
		for _, s := range sources {
			if s == "-" {
				return &userFacingError{title: "can't mix - (stdin) with file paths for supervisord import"}
			}
		}
		res, err = importer.ParseSupervisordFiles(sources, svOpts)
	}
	if err != nil {
		return &userFacingError{title: "failed to read supervisord config", details: err.Error()}
	}
	return emitImport(stdout, stderr, stdin, res, sourceSupervisord, f, opts)
}

// checkImportFlags rejects a combination that can't mean anything, before the
// source is even opened. A dry run's entire output *is* the summary, so
// suppressing it would leave a command that reads a file, writes nothing, and
// says nothing — a request the operator can't have meant.
func checkImportFlags(opts importOpts) error {
	if opts.dryRun && opts.quiet {
		return &userFacingError{
			title:   "--dry-run and --quiet contradict each other",
			details: "A dry run writes nothing, so the summary is all it has to show you. Drop one of the two.",
		}
	}
	return nil
}

// ownedEntries snapshots what the live config already owns, so a two-tier
// re-import skips a job it already holds and renames a genuine clash instead of
// colliding on the merged load. Only the two-tier `--write` path merges into an
// existing config; -o and stdout produce a standalone file that reserves
// nothing. A config that doesn't load reserves nothing either — the write is
// gated on the merged load anyway, and configedit reports an already-broken
// config as such rather than blaming the import.
func ownedEntries(f Flags, opts importOpts) importer.Owned {
	if !opts.write || opts.output != "" {
		return nil
	}
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		return nil
	}
	return importer.OwnedFrom(cfg.Tasks)
}

// resolveImportSource returns the given file path or "-" (stdin) for piped input.
func resolveImportSource(args []string, stdin *os.File, what string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	if !isatty.IsTerminal(stdin.Fd()) {
		return "-", nil
	}
	return "", &userFacingError{
		title:   "no " + what + " given",
		details: "Pass a file path, or pipe one in — e.g. `crontab -l | runwisp import cron`.",
	}
}

// resolveCronOptions decides whether to parse as a system crontab.
//
// A file path is one of this machine's crontabs, so the user column of a system
// line is checked against the account database — the same check the live
// include_cron loader makes, which is what keeps `import cron` and `promote`
// agreeing with what the daemon would schedule. A piped crontab is not
// necessarily ours: `crontab -l` from another host names accounts that don't
// exist here and legitimately so, so that case stays shape-only.
func resolveCronOptions(source string, systemFlag, systemSet bool) importer.CronOptions {
	opts := importer.CronOptions{}
	if source != "" && source != "-" {
		opts.UserExists = importer.SystemUserExists
	}
	switch {
	case systemSet:
		opts.System = systemFlag
	case importer.IsSystemCrontabPath(source):
		opts.System = true
	default:
		opts.Detect = true
	}
	return opts
}

// openImportSource returns a reader for a file path, or stdin when source is
// "-" (or empty). The returned close function is always safe to call.
func openImportSource(source string, stdin *os.File) (io.Reader, func(), error) {
	if source == "-" || source == "" {
		return stdin, func() {}, nil
	}
	file, err := os.Open(source)
	if err != nil {
		return nil, func() {}, &userFacingError{
			title:   fmt.Sprintf("can't open %s", source),
			details: err.Error(),
		}
	}
	return file, func() { _ = file.Close() }, nil
}

// emitImport renders the result, validates it, and delivers it: stdout by
// default, a standalone file with -o, or the two-tier managed layout with
// --write. The summary always goes to stderr.
func emitImport(stdout, stderr io.Writer, stdin *os.File, res *importer.Result, src importSource, f Flags, opts importOpts) error {
	// Prepend the schema directive so the imported file is editor-validated the
	// moment it lands, just like a scaffolded one. It is a TOML comment.
	toml := config.SchemaDirective + res.TOML()
	rep := importReport{res: res, source: src, validationErr: validateGeneratedTOML(toml)}

	// --dry-run only has something to hold back when a write was going to happen.
	// Plain stdout mode already writes nothing and already says so, so it keeps its
	// own epilogue rather than growing a second way to phrase the same thing.
	if opts.dryRun && (opts.write || opts.output != "") {
		return printImportPlan(stderr, rep, f, opts)
	}

	// --write (without an explicit -o path) installs the import in the two-tier
	// managed layout: tasks land in the machine-owned runwisp.d/imported.toml and
	// the root config's include is wired to pick them up. -o always means "give
	// me a standalone file at this path", the unchanged single-file flow.
	if opts.write && opts.output == "" {
		return stageImport(stderr, f.CfgFile, toml, rep, opts)
	}

	if opts.output == "" {
		// stdout mode: TOML to stdout, summary to stderr.
		if _, err := io.WriteString(stdout, toml); err != nil {
			return err
		}
		printImportSummary(stderr, rep, opts, singleFileEpilogue(rep, ""))
		return nil
	}

	if err := confirmAndWrite(stderr, stdin, opts.output, toml, opts); err != nil {
		return err
	}
	printImportSummary(stderr, rep, opts, singleFileEpilogue(rep, opts.output))
	return nil
}

// printImportPlan is --dry-run for a saving import: the same report a real run
// prints, with an epilogue naming every file that run would touch and then
// saying plainly that none of them were.
//
// What a dry run does prove is that the generated content loads —
// rep.validationErr comes from the same temp-file round-trip either way. What it
// can't prove for the two-tier layout is that the *merge* loads, because that
// means writing both files and loading the result, which is the thing being
// deferred. The one merge failure knowable up front — a root config that doesn't
// load as it stands — is reported.
func printImportPlan(stderr io.Writer, rep importReport, f Flags, opts importOpts) error {
	if opts.output != "" {
		_, err := os.Stat(opts.output)
		printImportSummary(stderr, rep, opts,
			singleFilePlanEpilogue(rep, opts.output, err == nil && !opts.force))
		return nil
	}

	layout := configedit.NewLayout(f.CfgFile)
	plan, err := configedit.PlanStage(layout)
	if err != nil {
		return stageError(err, layout)
	}
	printImportSummary(stderr, rep, opts, twoTierPlanEpilogue(rep, plan, layout))
	return nil
}

// stageImport installs the import in the two-tier managed layout and reports
// what happened. configedit owns the write itself — the atomic two-file
// transaction, the include wiring, and the merged-load gate; this function maps
// its outcomes onto the CLI's voice.
//
// rep.validationErr is the pre-known validation error of the generated content
// itself (an unparseable cron that became a `# TODO`). When set, the write skips
// the load gate so the files are kept for the operator to fix in place —
// matching the single-file --write behavior, and the reason the TODO was emitted
// at all.
func stageImport(stderr io.Writer, rootPath, stagingContent string, rep importReport, opts importOpts) error {
	layout := configedit.NewLayout(rootPath)
	staged, err := configedit.Stage(configedit.StageRequest{
		Layout:   layout,
		Staging:  []byte(stagingContent),
		Validate: rep.validationErr == nil,
	})
	if err != nil {
		return stageError(err, layout)
	}
	printImportSummary(stderr, rep, opts, twoTierEpilogue(rep, staged, layout))
	return nil
}

// stageError translates a configedit failure into the CLI's voice. Every case
// here left the operator's config exactly as it was.
func stageError(err error, layout configedit.Layout) error {
	rootName := filepath.Base(layout.RootPath)

	if errors.Is(err, configedit.ErrIncludeNeedsManualWiring) {
		return &userFacingError{
			title: fmt.Sprintf("%s already sets a custom [daemon].include", rootName),
			details: fmt.Sprintf(
				"Add %q to that list, then re-run `runwisp import cron --write`. Nothing was written.",
				config.StagingIncludeGlob),
		}
	}
	var conflict *configedit.ConflictError
	if errors.As(err, &conflict) {
		return &userFacingError{
			title:   "import conflicts with your existing config — nothing was written",
			details: conflict.Err.Error(),
		}
	}
	var preexisting *configedit.PreexistingError
	if errors.As(err, &preexisting) {
		return &userFacingError{
			title: fmt.Sprintf("%s didn't load before this import either — nothing was written", rootName),
			details: preexisting.Err.Error() +
				"\n\nThat's a pre-existing problem, not a conflict with the import. Fix it, then re-run.",
		}
	}
	return configEditError(err, layout)
}

// configEditError is the tail every config-writing command shares: a filesystem
// failure names the file it couldn't touch, and anything else falls back to the
// root config. Both `import --write` and `promote` end here, so the two never
// drift in how they phrase "we couldn't update your config".
func configEditError(err error, layout configedit.Layout) error {
	var write *configedit.WriteError
	if errors.As(err, &write) {
		return &userFacingError{
			title:   fmt.Sprintf("can't write %s", write.Path),
			details: write.Err.Error(),
		}
	}
	return &userFacingError{
		title:   fmt.Sprintf("can't update %s", filepath.Base(layout.RootPath)),
		details: err.Error(),
	}
}

// confirmAndWrite writes toml to target, prompting before clobbering an
// existing file on a terminal and refusing on a non-terminal unless --force.
func confirmAndWrite(stderr io.Writer, stdin *os.File, target, toml string, opts importOpts) error {
	if _, err := os.Stat(target); err == nil && !opts.force {
		prompter := autostart.NewStdioPrompter(stdin, stderr, isatty.IsTerminal(stdin.Fd()), false)
		ok, err := prompter.Confirm(fmt.Sprintf("%s already exists. Overwrite?", target), false)
		if err != nil {
			if errors.Is(err, autostart.ErrNeedsYes) {
				return &userFacingError{
					title:   fmt.Sprintf("%s already exists", target),
					details: "Re-run with --force to overwrite it, or -o to write somewhere else.",
				}
			}
			return err
		}
		if !ok {
			return &userFacingError{title: "aborted — nothing was written"}
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(target, []byte(toml), 0o644); err != nil {
		return &userFacingError{
			title:   fmt.Sprintf("can't write %s", target),
			details: err.Error(),
		}
	}
	return nil
}

// validateGeneratedTOML round-trips the generated config through config.Load to
// prove it parses and validates, returning any error so the summary can flag
// it. The content is written to a temp file because config.Load works on paths.
func validateGeneratedTOML(toml string) error {
	dir, err := os.MkdirTemp("", "runwisp-import-*")
	if err != nil {
		return nil // can't validate; don't block the import
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "runwisp.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		return nil
	}
	_, err = config.Load(path)
	return err
}
