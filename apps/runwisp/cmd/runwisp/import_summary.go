// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
	"github.com/runwisp/runwisp/internal/importer"
)

// This file owns how an import *reads*: the counts, the per-job rows, the
// file-level notes, the verdict, and the epilogue that tells the operator where
// their jobs landed and what to do next. Directive #1 lives here as much as
// anywhere — an import that silently drops a job is a bug, so every row the
// parser opened has to reach this output. The layout itself lives in
// import_report.go, which is pure; this file is the part that writes.

// importStyles carries the small palette shared by the import summaries.
type importStyles struct {
	ok, changed, attn, dim lipgloss.Style
}

// newImportStyles builds the palette for one specific destination. The color
// profile is detected from w rather than from os.Stdout, because these summaries
// go to stderr and `runwisp import cron 2> report.txt` must not collect escape
// sequences.
func newImportStyles(w io.Writer) importStyles {
	r := rendererFor(w)
	return importStyles{
		ok:      r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "2", Dark: "10"}),
		changed: r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "6", Dark: "14"}),
		attn:    r.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "3", Dark: "11"}),
		dim:     r.NewStyle().Faint(true),
	}
}

// importReport is everything the summary renders: the parsed result, what it was
// parsed from, and whether the generated config actually loads. validationErr
// lives here rather than only inside the epilogue closure because the verdict
// line needs it too — see importFooterLine.
type importReport struct {
	res           *importer.Result
	sourceLabel   string
	validationErr error
}

// emitted is what this import actually produced: the tasks and services that
// landed in a file. A skipped job produced nothing, so counting it would
// overstate what happened.
func (rep importReport) emitted() (tasks, services int) {
	t := rep.res.Tally()
	return t.Tasks, t.Services
}

// blockingRows counts the jobs that need a human. It also decides whether the
// epilogue's raw config.Load dump would tell the operator anything they haven't
// already been told in the terms of their own file.
func (rep importReport) blockingRows() int { return rep.res.Tally().Blocked }

// printImportSummary writes the human-friendly overview to stderr: the counts, a
// row per job with the command that will run, the file-level notes, the verdict,
// and then the epilogue for however this import was delivered — the one part
// that differs between stdout, -o, and the two-tier layout.
//
// --quiet silences the inventory, not the alarm. A clean import prints nothing,
// but one that needs a fix still names the jobs that need it: writing a config
// that won't load and saying nothing is exactly the silent failure this command
// exists to prevent, and --quiet is the flag most likely to be in a script.
func printImportSummary(w io.Writer, rep importReport, opts importOpts, epilogue func(io.Writer, importStyles)) {
	st := newImportStyles(w)
	width := terminalWidth(w)
	items := rep.res.Items()

	if opts.quiet {
		if rep.blockingRows() == 0 && rep.validationErr == nil {
			return
		}
		items = onlyBlocking(items)
	} else {
		fmt.Fprintf(w, "\nImported %s → %s\n", rep.sourceLabel, pluralizeCounts(rep.emitted()))
	}

	lay := newItemLayout(items, width)
	for _, it := range items {
		writeLines(w, importItemLines(it, lay, st))
	}
	if !opts.quiet {
		writeLines(w, importNoteLines(rep.res.Notes(), width, st))
	}
	if footer := importFooterLine(rep.res, rep.validationErr); footer != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, footer)
	}

	fmt.Fprintln(w)
	epilogue(w, st)
}

// writeLines writes pre-formatted lines, each of which already carries its own
// indentation.
func writeLines(w io.Writer, lines []string) {
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}

// singleFileEpilogue reports a standalone import: printed to stdout (target "")
// or saved to the -o path. Neither is loaded by the daemon yet, so both point at
// `runwisp validate`.
func singleFileEpilogue(rep importReport, target string) func(io.Writer, importStyles) {
	return func(w io.Writer, st importStyles) {
		if rep.validationErr != nil {
			// When the rows above already carry the parser's own words for what's
			// wrong, repeating config.Load's error is noise. With nothing flagged,
			// it's the only clue there is.
			if rep.blockingRows() > 0 {
				fmt.Fprintln(w, "Fix the items above, then re-run `runwisp validate`.")
				return
			}
			fmt.Fprintf(w, "%s the generated config didn't validate yet:\n  %s\n",
				st.attn.Render("!"), rep.validationErr.Error())
			fmt.Fprintln(w, "Fix that, then re-run `runwisp validate`.")
			return
		}
		if target != "" {
			fmt.Fprintf(w, "Wrote %s. Review it, then run `runwisp validate`.\n", target)
			return
		}
		fmt.Fprintln(w, "Review the TOML above, save it as runwisp.toml, then run `runwisp validate`.")
	}
}

// twoTierEpilogue reports a two-tier `--write`: where the staging file landed,
// what happened to the root config, and the nudge toward `runwisp promote`.
func twoTierEpilogue(rep importReport, staged configedit.StageResult, layout configedit.Layout) func(io.Writer, importStyles) {
	return func(w io.Writer, st importStyles) {
		fmt.Fprintf(w, "Staged %s in %s\n", pluralizeCounts(rep.emitted()), staged.StagingPath)
		glob := config.StagingIncludeGlob
		switch staged.Root {
		case configedit.RootCreated:
			fmt.Fprintf(w, "Created %s and wired it to load %s.\n", layout.RootPath, glob)
		case configedit.RootWired:
			fmt.Fprintf(w, "Wired %s to load %s.\n", layout.RootPath, glob)
		case configedit.RootAlreadyIncluded:
			fmt.Fprintf(w, "%s already loads %s.\n", layout.RootPath, glob)
		}

		if rep.validationErr != nil {
			fmt.Fprintln(w)
			if rep.blockingRows() == 0 {
				fmt.Fprintf(w, "%s the staged config didn't validate yet:\n  %s\n",
					st.attn.Render("!"), rep.validationErr.Error())
			}
			fmt.Fprintf(w, "Resolve the # TODO items in %s, then run `runwisp validate`.\n", staged.StagingPath)
			return
		}

		fmt.Fprintln(w)
		fmt.Fprintf(w, "Validated — the daemon loads these on next start or `runwisp reload`.\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "They show as %s: imported, not yet native. Graduate one into\n", st.dim.Render("staged"))
		fmt.Fprintf(w, "%s when you want to own it:\n", filepath.Base(layout.RootPath))
		fmt.Fprintln(w, "  runwisp promote <name>")
	}
}

// pluralizeCounts names a count of tasks and services, shared by import and
// promote so the two phrase "what moved" identically.
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
