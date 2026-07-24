// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/config"
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
# TODO comment and a note in the summary, so nothing is silently dropped.`,
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
	}
	importCronCmd.Flags().BoolVar(&importFlags.system, "system", false, "force system-crontab parsing (user column); --system=false forces per-user. Auto-detected when unset")

	importCmd.AddCommand(importCronCmd)
	importCmd.AddCommand(importSupervisordCmd)
}

func runImportCron(stdout, stderr io.Writer, stdin *os.File, source string, cronOpts importer.CronOptions, f Flags, opts importOpts) error {
	// A two-tier --write dedupes against what the root config already owns, so a
	// re-import after `promote` skips the same job and renames a genuine clash
	// instead of colliding on the merged load.
	if opts.write && opts.output == "" {
		cronOpts.Existing = existingEntryCommands(f.CfgFile)
	}

	r, closeFn, err := openImportSource(source, stdin)
	if err != nil {
		return err
	}
	defer closeFn()

	res, err := importer.ParseCrontab(r, cronOpts)
	if err != nil {
		return &userFacingError{title: "failed to read crontab", details: err.Error()}
	}
	return emitImport(stdout, stderr, stdin, res, "crontab", f, opts)
}

func runImportSupervisord(stdout, stderr io.Writer, stdin *os.File, sources []string, f Flags, opts importOpts) error {
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
		res, err = importer.ParseSupervisordReader(stdin)
	case len(sources) == 1 && sources[0] == "-":
		res, err = importer.ParseSupervisordReader(stdin)
	default:
		for _, s := range sources {
			if s == "-" {
				return &userFacingError{title: "can't mix - (stdin) with file paths for supervisord import"}
			}
		}
		res, err = importer.ParseSupervisordFiles(sources)
	}
	if err != nil {
		return &userFacingError{title: "failed to read supervisord config", details: err.Error()}
	}
	return emitImport(stdout, stderr, stdin, res, "supervisord config", f, opts)
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
func resolveCronOptions(source string, systemFlag, systemSet bool) importer.CronOptions {
	switch {
	case systemSet:
		return importer.CronOptions{System: systemFlag}
	case isSystemCrontabPath(source):
		return importer.CronOptions{System: true}
	default:
		return importer.CronOptions{Detect: true}
	}
}

// isSystemCrontabPath reports whether a source path is a conventional
// system-crontab location, whose lines carry a user column.
func isSystemCrontabPath(source string) bool {
	if source == "" || source == "-" {
		return false
	}
	clean := filepath.Clean(source)
	if clean == "/etc/crontab" {
		return true
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "cron.d" {
			return true
		}
	}
	return false
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
func emitImport(stdout, stderr io.Writer, stdin *os.File, res *importer.Result, sourceLabel string, f Flags, opts importOpts) error {
	// Prepend the schema directive so the imported file is editor-validated the
	// moment it lands, just like a scaffolded one. It is a TOML comment.
	toml := config.SchemaDirective + res.TOML()
	validationErr := validateGeneratedTOML(toml)

	// --write (without an explicit -o path) installs the import in the two-tier
	// managed layout: tasks land in the machine-owned runwisp.d/imported.toml and
	// the root config's include is wired to pick them up. -o always means "give
	// me a standalone file at this path", the unchanged single-file flow.
	if opts.write && opts.output == "" {
		return emitTwoTier(stderr, f.CfgFile, toml, res, sourceLabel, validationErr, opts)
	}

	if opts.output == "" {
		// stdout mode: TOML to stdout, summary to stderr.
		if _, err := io.WriteString(stdout, toml); err != nil {
			return err
		}
		printImportSummary(stderr, res, sourceLabel, "", validationErr, opts)
		return nil
	}

	if err := confirmAndWrite(stderr, stdin, opts.output, toml, opts); err != nil {
		return err
	}
	printImportSummary(stderr, res, sourceLabel, opts.output, validationErr, opts)
	return nil
}

// confirmAndWrite writes toml to target, prompting before clobbering an
// existing file on a terminal and refusing on a non-terminal unless --force.
func confirmAndWrite(stderr io.Writer, stdin *os.File, target, toml string, opts importOpts) error {
	if _, err := os.Stat(target); err == nil && !opts.force {
		if !isatty.IsTerminal(stdin.Fd()) {
			return &userFacingError{
				title:   fmt.Sprintf("%s already exists", target),
				details: "Re-run with --force to overwrite it, or -o to write somewhere else.",
			}
		}
		fmt.Fprintf(stderr, "%s already exists. Overwrite? [y/N] ", target)
		answer, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read prompt response: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
		default:
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

// importStyles carries the small palette shared by the import summaries.
type importStyles struct {
	ok, attn, dim lipgloss.Style
}

func newImportStyles() importStyles {
	return importStyles{
		ok:   lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "2", Dark: "10"}),
		attn: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "3", Dark: "11"}),
		dim:  lipgloss.NewStyle().Faint(true),
	}
}

// printImportItems renders the per-item list (✓ clean, ! needs attention).
func printImportItems(w io.Writer, res *importer.Result, st importStyles) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, it := range res.Items() {
		mark := st.ok.Render("✓")
		if it.Attention {
			mark = st.attn.Render("!")
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", mark, it.Name, st.dim.Render(it.Schedule))
	}
	_ = tw.Flush()
}

// printImportNotes renders the notes block, if any.
func printImportNotes(w io.Writer, res *importer.Result, st importStyles) {
	if len(res.Notes) == 0 {
		return
	}
	fmt.Fprintln(w, "\nNotes:")
	for _, n := range res.Notes {
		bullet := st.dim.Render("•")
		if n.Level == importer.LevelAttention {
			bullet = st.attn.Render("!")
		}
		scope := ""
		if n.Scope != "" {
			scope = st.dim.Render("[" + n.Scope + "] ")
		}
		fmt.Fprintf(w, "  %s %s%s\n", bullet, scope, n.Message)
	}
}

// printImportSummary writes the human-friendly overview to stderr: counts, a
// per-item list (✓ clean, ! needs attention), the notes, and next steps.
func printImportSummary(w io.Writer, res *importer.Result, sourceLabel, target string, validationErr error, opts importOpts) {
	if opts.quiet {
		return
	}
	st := newImportStyles()

	tasks, services := res.Counts()
	fmt.Fprintf(w, "\nImported %s → %s\n", sourceLabel, pluralizeCounts(tasks, services))
	printImportItems(w, res, st)
	printImportNotes(w, res, st)

	fmt.Fprintln(w)
	if validationErr != nil {
		fmt.Fprintf(w, "%s the generated config didn't validate yet:\n  %s\n",
			st.attn.Render("!"), validationErr.Error())
		fmt.Fprintln(w, "Fix the items above, then re-run `runwisp validate`.")
		return
	}
	switch {
	case target != "":
		fmt.Fprintf(w, "Wrote %s. Review it, then run `runwisp validate`.\n", target)
	default:
		fmt.Fprintln(w, "Review the TOML above, save it as runwisp.toml, then run `runwisp validate`.")
	}
}

func pluralizeCounts(tasks, services int) string {
	var parts []string
	if tasks > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", tasks, plural(tasks, "task", "tasks")))
	}
	if services > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", services, plural(services, "service", "services")))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
