// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/runwisp/runwisp/internal/importer"
	"github.com/runwisp/runwisp/internal/textutil"
)

// This file lays out the import report. Every function here is pure — it takes
// the report plus an explicit width and returns lines — which is what lets the
// layout be asserted directly instead of through a human-text golden file.
//
// There is no tabwriter. text/tabwriter measures cells in runes and counts ANSI
// escapes as content, so a colored ✓ row and a bold ! row pad to different
// widths and the name column visibly jitters; a literal tab inside a `run`
// script would also become a column break. The column here is computed in
// display cells, and the mark is always exactly one cell wide.

const (
	itemMarkCol   = 2 // two leading spaces, then the mark
	itemNameStart = 4 // mark plus one space
	// The command and the difference bullets sit at fixed indents, independent of
	// the name column, so a 40-character name pushes only its own schedule right.
	itemCommandIndent  = 6
	itemCommandHanging = 8
	itemNoteIndent     = 6
	itemNoteHanging    = 8
	// fileNoteIndent is the shallower indent of the file-level Notes block, which
	// hangs off nothing.
	fileNoteIndent  = 2
	fileNoteHanging = 4
	// maxItemNameWidth caps the name column so one very long name doesn't push
	// every schedule off the right edge.
	maxItemNameWidth = 24
	itemNameGap      = 3
)

// itemLayout is the geometry the row block shares: how many cells the name
// column occupies, and how wide the terminal is (0 = unknown, don't wrap).
type itemLayout struct {
	nameCol int
	width   int
}

// newItemLayout sizes the name column to the widest label that will be printed,
// capped, so the schedules line up without a second pass.
func newItemLayout(items []importer.Item, width int) itemLayout {
	longest := 0
	for _, it := range items {
		if w := ansi.StringWidth(rowLabel(it)); w > longest {
			longest = w
		}
	}
	if longest > maxItemNameWidth {
		longest = maxItemNameWidth
	}
	return itemLayout{nameCol: longest + itemNameGap, width: width}
}

// rowLabel is what to call this row: the RunWisp name it got, or — when nothing
// was emitted — whatever the source file called it.
func rowLabel(it importer.Item) string {
	if it.Name != "" {
		return it.Name
	}
	return it.Source
}

// itemMark returns the one-cell mark for a status. `~` is cyan rather than
// yellow on purpose: "imported, but different" is information, and `!` should
// stay the only mark that reads as an alarm.
//
// The unreachable default is `?`, not `✓`. A fifth ItemStatus added later must
// not inherit the mark that means "nothing to look at here" — that is how a
// difference goes unnoticed, and it is the bug this report was built to prevent.
func itemMark(s importer.ItemStatus, st importStyles) string {
	switch s {
	case importer.StatusClean:
		return st.ok.Render("✓")
	case importer.StatusChanged:
		return st.changed.Render("~")
	case importer.StatusBlocked:
		return st.attn.Render("!")
	case importer.StatusSkipped:
		return st.dim.Render("-")
	default:
		return st.attn.Render("?")
	}
}

// importItemLines renders one report row: the mark, the name, the schedule, the
// command that will run, and any differences nested underneath.
//
// The command is never truncated. A trailing `>> /var/log/x.log 2>&1` changes
// what a job does with its output, so hiding it to save a column would be
// hiding the thing the operator is here to check.
func importItemLines(it importer.Item, lay itemLayout, st importStyles) []string {
	status := it.Status()
	mark := itemMark(status, st)
	label := rowLabel(it)

	// A skipped row is the one shape exception: the reason sits where the schedule
	// would, and nothing is nested, because nothing about it will run.
	if status == importer.StatusSkipped {
		return []string{headLine(mark, padTo(label, lay.nameCol)+st.dim.Render(it.SkipReason()))}
	}

	var lines []string
	if it.Schedule == "" {
		// Nothing follows the label, so it may use the full width and wrap under
		// itself — this is the row for a line the parser couldn't read at all,
		// whose label is the operator's own crontab line.
		wrapped := wrapAfter(label, lay.width, 0, itemNameStart)
		wrapped[0] = headLine(mark, wrapped[0])
		lines = append(lines, wrapped...)
	} else {
		lines = append(lines, headLine(mark, padTo(label, lay.nameCol)+st.dim.Render(it.Schedule)))
	}

	// No command row rather than an invented "(no command)": on a blocked row the
	// nested note already says the program had none.
	if it.Run != "" {
		lines = append(lines, wrapAfter(it.Run, lay.width, itemCommandIndent, itemCommandHanging)...)
	}
	for _, n := range it.Notes {
		lines = append(lines, noteLines(n, lay.width, st, itemNoteIndent, itemNoteHanging)...)
	}
	return lines
}

// headLine assembles a row's first line: two spaces, the one-cell mark, a space,
// then the rest.
func headLine(mark, rest string) string {
	return strings.Repeat(" ", itemMarkCol) + mark + " " + rest
}

// importNoteLines renders the file-level Notes block — the observations about
// the source file's structure rather than about any one job. Returns nil when
// there are none, so the caller never prints an empty heading.
func importNoteLines(notes []importer.Note, width int, st importStyles) []string {
	if len(notes) == 0 {
		return nil
	}
	lines := []string{"", "Notes:"}
	for _, n := range notes {
		lines = append(lines, noteLines(n, width, st, fileNoteIndent, fileNoteHanging)...)
	}
	return lines
}

// noteLines renders one note as a bullet at `indent` with its text hanging to
// `hanging`. Only the bullet is styled; the wrapped body stays plain so a line
// break can never land inside an escape sequence.
func noteLines(n importer.Note, width int, st importStyles, indent, hanging int) []string {
	bullet := st.dim.Render("•")
	if n.Blocking() {
		bullet = st.attn.Render("!")
	}
	lines := wrapAfter(n.Message, width, 0, hanging)
	lines[0] = strings.Repeat(" ", indent) + bullet + " " + lines[0]
	return lines
}

// importFooterLine is the one-line verdict, or "" when nothing is blocked.
//
// It needs two phrasings because blocking does not imply won't-load: a program
// with no `command=` emits `run = ""` and the load genuinely fails, while an
// unresolved `%(ENV_x)s` emits a syntactically fine run line that loads and then
// runs the wrong command. Demoting the second to a mere difference would bury
// the scarier of the two, so the mark stays and the wording adapts.
func importFooterLine(res *importer.Result, validationErr error) string {
	blocked := res.Tally().Blocked
	if blocked == 0 {
		return ""
	}
	// "item", not "task": a blocked row may be a crontab line that never became a
	// task at all, and it pairs with the epilogue's "Fix the items above".
	subject := textutil.Count(blocked, "item", "items")
	if validationErr != nil {
		return subject + " " + textutil.Pluralize(blocked, "needs", "need") + " a fix before this config loads."
	}
	return subject + " " + textutil.Pluralize(blocked, "carries", "carry") + " a # TODO — fix it before you rely on it."
}

// onlyBlocking keeps the rows that need a human. It is how --quiet silences the
// inventory without silencing the alarm — same renderer, fewer rows.
func onlyBlocking(items []importer.Item) []importer.Item {
	out := make([]importer.Item, 0, len(items))
	for _, it := range items {
		if it.Status() == importer.StatusBlocked {
			out = append(out, it)
		}
	}
	return out
}
