// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
	"github.com/runwisp/runwisp/internal/importer"
)

// This file owns how an import *reads*: the counts, the per-item ✓/! list, the
// notes, and the epilogue that tells the operator where their jobs landed and
// what to do next. Directive #1 lives here as much as anywhere — an import that
// silently drops a job is a bug, so everything the parser flagged has to reach
// this output.

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
// per-item list (✓ clean, ! needs attention), the notes, and then the epilogue
// for however this import was delivered — the one part that differs between
// stdout, -o, and the two-tier layout.
func printImportSummary(w io.Writer, res *importer.Result, sourceLabel string, opts importOpts, epilogue func(io.Writer, importStyles)) {
	if opts.quiet {
		return
	}
	st := newImportStyles()

	tasks, services := res.Counts()
	fmt.Fprintf(w, "\nImported %s → %s\n", sourceLabel, pluralizeCounts(tasks, services))
	printImportItems(w, res, st)
	printImportNotes(w, res, st)

	fmt.Fprintln(w)
	epilogue(w, st)
}

// singleFileEpilogue reports a standalone import: printed to stdout (target "")
// or saved to the -o path. Neither is loaded by the daemon yet, so both point at
// `runwisp validate`.
func singleFileEpilogue(target string, validationErr error) func(io.Writer, importStyles) {
	return func(w io.Writer, st importStyles) {
		if validationErr != nil {
			fmt.Fprintf(w, "%s the generated config didn't validate yet:\n  %s\n",
				st.attn.Render("!"), validationErr.Error())
			fmt.Fprintln(w, "Fix the items above, then re-run `runwisp validate`.")
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
func twoTierEpilogue(res *importer.Result, staged configedit.StageResult, layout configedit.Layout, contentErr error) func(io.Writer, importStyles) {
	return func(w io.Writer, st importStyles) {
		tasks, services := res.Counts()
		fmt.Fprintf(w, "Staged %s in %s\n", pluralizeCounts(tasks, services), staged.StagingPath)
		glob := config.StagingIncludeGlob
		switch staged.Root {
		case configedit.RootCreated:
			fmt.Fprintf(w, "Created %s and wired it to load %s.\n", layout.RootPath, glob)
		case configedit.RootWired:
			fmt.Fprintf(w, "Wired %s to load %s.\n", layout.RootPath, glob)
		case configedit.RootAlreadyIncluded:
			fmt.Fprintf(w, "%s already loads %s.\n", layout.RootPath, glob)
		}

		if contentErr != nil {
			fmt.Fprintln(w)
			fmt.Fprintf(w, "%s some tasks need a fix before the config validates:\n  %s\n",
				st.attn.Render("!"), contentErr.Error())
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
