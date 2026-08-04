// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/runwisp/runwisp/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plainStyles is the palette a non-terminal destination gets: every Render is a
// no-op, so the layout can be asserted with exact equality.
func plainStyles() importStyles { return newImportStyles(&bytes.Buffer{}) }

// TestImportItemLinesMarksAndShape pins the four marks and the row shape at
// width 0, where nothing wraps. Exact equality is right here precisely because
// the formatter is pure and the width is an explicit parameter.
func TestImportItemLinesMarksAndShape(t *testing.T) {
	items := []importer.Item{
		{Source: "backup", Name: "backup", Kind: "task", Schedule: "0 3 * * *",
			Run: "/usr/local/bin/backup.sh --full"},
		{Source: "reaper", Name: "tmp-reaper", Kind: "task", Schedule: "0 4 * * *",
			Run: "/usr/bin/find /tmp -delete",
			Notes: []importer.Note{
				{Kind: importer.NoteLogsDropped, Message: "log file settings were dropped."},
			}},
		{Source: "report", Name: "nightly", Kind: "task", Schedule: "0 0 * * 8",
			Run: "/opt/bin/report.sh",
			Notes: []importer.Note{
				{Kind: importer.NoteCronUnparseable, Message: "cron expression 0 0 * * 8 didn't parse."},
			}},
		{Source: "deploy", Notes: []importer.Note{
			{Kind: importer.NoteAlreadyDefined, Message: "already defined in runwisp.toml with the same command"},
		}},
	}

	st := plainStyles()
	lay := newItemLayout(items, 0)
	var got []string
	for _, it := range items {
		got = append(got, importItemLines(it, lay, st)...)
	}

	want := []string{
		"  ✓ backup       0 3 * * *",
		"      /usr/local/bin/backup.sh --full",
		"  ~ tmp-reaper   0 4 * * *",
		"      /usr/bin/find /tmp -delete",
		"      • log file settings were dropped.",
		"  ! nightly      0 0 * * 8",
		"      /opt/bin/report.sh",
		"      ! cron expression 0 0 * * 8 didn't parse.",
		"  - deploy       already defined in runwisp.toml with the same command",
	}
	require.Equal(t, want, got)
}

// TestImportItemMarkNeverDefaultsToClean guards the renderer half of the same
// rule the derived status guards in the importer: `✓` must be reachable only by
// actually being clean. A fifth ItemStatus added later falls into the default
// arm, and if that arm rendered ✓ the new state would announce itself as
// "nothing to look at" — the one thing this report may never say wrongly.
func TestImportItemMarkNeverDefaultsToClean(t *testing.T) {
	st := plainStyles()
	clean := itemMark(importer.StatusClean, st)
	require.Equal(t, "✓", clean)

	// Every value outside the enum, including a future addition just past its end.
	for _, s := range []importer.ItemStatus{-1, 4, 99} {
		assert.NotEqual(t, clean, itemMark(s, st), "ItemStatus(%d) rendered as clean", s)
	}
}

// parseCronForReport builds a real report from a crontab, for the assertions
// that are about what the importer derives rather than about layout.
func parseCronForReport(t *testing.T, in string) *importer.Result {
	t.Helper()
	res, err := importer.ParseCrontab(strings.NewReader(in), importer.CronOptions{})
	require.NoError(t, err)
	return res
}

// TestImportItemLinesWrapsWithoutLosingTheTail is the reason rows wrap rather
// than truncate: the `2>&1` at the end of a redirect changes what the job does
// with its output, so it's the last thing that may be dropped to save a column.
func TestImportItemLinesWrapsWithoutLosingTheTail(t *testing.T) {
	run := "/usr/bin/find /tmp -type f -mtime +7 -delete " +
		strings.Repeat("--exclude /tmp/keep ", 8) + ">> /var/log/reap.log 2>&1"
	it := importer.Item{Source: "find", Name: "tmp-reaper", Kind: "task",
		Schedule: "0 4 * * *", Run: run}

	const width = 60
	lines := importItemLines(it, newItemLayout([]importer.Item{it}, width), plainStyles())

	require.Greater(t, len(lines), 2, "a long command must wrap onto continuation lines")
	for _, line := range lines {
		assert.LessOrEqual(t, ansi.StringWidth(line), width, "line exceeds the width: %q", line)
	}
	for _, line := range lines[2:] {
		assert.True(t, strings.HasPrefix(line, strings.Repeat(" ", itemCommandHanging)),
			"continuation should indent to %d: %q", itemCommandHanging, line)
	}
	// The command survives reassembly in full, redirect and all.
	var body strings.Builder
	for _, line := range lines[1:] {
		body.WriteString(strings.TrimSpace(line) + " ")
	}
	assert.Contains(t, body.String(), ">> /var/log/reap.log 2>&1")
	assert.Equal(t, strings.Fields(run), strings.Fields(body.String()))
}

// TestImportItemLinesUnwrappedWithoutAWidth is the pipe case: with no width
// there is nothing to wrap to, so the command stays on one line and
// `runwisp import cron 2>&1 | grep '2>&1'` still finds it.
func TestImportItemLinesUnwrappedWithoutAWidth(t *testing.T) {
	run := "/usr/bin/reap " + strings.Repeat("--flag ", 40) + ">> /var/log/reap.log 2>&1"
	it := importer.Item{Source: "reap", Name: "reap", Kind: "task",
		Schedule: "@daily", Run: run}

	lines := importItemLines(it, newItemLayout([]importer.Item{it}, 0), plainStyles())
	require.Len(t, lines, 2)
	assert.Equal(t, strings.Repeat(" ", itemCommandIndent)+run, lines[1])
}

// TestImportItemLinesExpandsTabs covers a multi-line `run` script: a literal tab
// used to be a column break under tabwriter, and it makes the wrap arithmetic
// fiction either way.
func TestImportItemLinesExpandsTabs(t *testing.T) {
	it := importer.Item{Source: "deploy", Name: "deploy", Kind: "task", Schedule: "@reboot",
		Run: "cd /srv\n\tmake build\n\tmake deploy"}

	lines := importItemLines(it, newItemLayout([]importer.Item{it}, 0), plainStyles())
	require.Len(t, lines, 4, "each script line gets its own output line")
	for _, line := range lines {
		assert.NotContains(t, line, "\t", "tabs must be expanded before layout")
	}
	assert.Equal(t, "      cd /srv", lines[1])
	// The leading tab became four spaces on top of the continuation indent.
	assert.Equal(t, "            make build", lines[2])
}

// TestImportItemLayoutMeasuresInCells pins ansi.StringWidth over len: a CJK name
// is 3 runes and 9 bytes but occupies 6 cells, and a column sized by either of
// the wrong two numbers puts the schedules out of line.
func TestImportItemLayoutMeasuresInCells(t *testing.T) {
	items := []importer.Item{
		{Source: "备份", Name: "备份", Kind: "task", Schedule: "0 3 * * *"},
		{Source: "abc", Name: "abc", Kind: "task", Schedule: "0 4 * * *"},
	}
	lay := newItemLayout(items, 0)
	require.Equal(t, 4+itemNameGap, lay.nameCol, "两 wide glyphs are 4 cells, not 2 runes or 6 bytes")

	st := plainStyles()
	first := importItemLines(items[0], lay, st)[0]
	second := importItemLines(items[1], lay, st)[0]
	assert.Equal(t, ansi.StringWidth(first), ansi.StringWidth(second),
		"schedules must start at the same cell:\n%q\n%q", first, second)
}

// TestImportItemMarkAlignmentWithColor is the tabwriter bug. text/tabwriter
// measures a cell in runes and counts ANSI escapes as content, so a green "✓"
// (15 runes of escape) and a bold yellow "!" (17) padded to different widths and
// the name column jittered by two cells depending on the mark. Nothing else in
// the suite enables a color profile, which is why it went unnoticed.
func TestImportItemMarkAlignmentWithColor(t *testing.T) {
	st := colorStyles()
	items := []importer.Item{
		{Source: "clean", Name: "clean", Kind: "task", Schedule: "0 3 * * *"},
		{Source: "bad", Name: "bad", Kind: "task", Schedule: "0 4 * * *",
			Notes: []importer.Note{{Kind: importer.NoteCronUnparseable, Message: "nope"}}},
	}
	lay := newItemLayout(items, 0)

	first := ansi.Strip(importItemLines(items[0], lay, st)[0])
	second := ansi.Strip(importItemLines(items[1], lay, st)[0])
	require.NotEqual(t, first, importItemLines(items[0], lay, st)[0], "the palette must actually be styling")
	// Compared in cells, not byte offsets: "✓" is three bytes and "!" is one, so
	// a byte index would report a two-column difference that isn't there — the
	// same confusion between bytes and cells that broke the tabwriter version.
	assert.Equal(t, scheduleColumn(first, "0 3"), scheduleColumn(second, "0 4"),
		"the schedule column must not move with the mark:\n%q\n%q", first, second)
}

// scheduleColumn reports which display cell the schedule starts at.
func scheduleColumn(line, schedule string) int {
	return ansi.StringWidth(line[:strings.Index(line, schedule)])
}

// colorStyles forces a real color profile, which no other test in this package
// does — a layout bug that only shows up in color needs one to be caught.
// SetColorProfile rather than the NewRenderer option: lipgloss resolves an
// unforced profile through EnvColorProfile, which reports Ascii for anything that
// isn't a terminal, so io.Discard would render plain either way.
func colorStyles() importStyles {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	return importStyles{
		ok:      r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "2", Dark: "10"}),
		changed: r.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "6", Dark: "14"}),
		attn:    r.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "3", Dark: "11"}),
		dim:     r.NewStyle().Faint(true),
	}
}

// TestImportFooterLineDistinguishesLoadFailureFromTODO covers the two phrasings.
// Blocking does not imply won't-load: a program with no command emits `run = ""`
// and the load fails, while an unresolved %(ENV_x)s emits a fine run line that
// loads and then runs the wrong thing.
func TestImportFooterLineDistinguishesLoadFailureFromTODO(t *testing.T) {
	res := parseCronForReport(t, "99 99 * * * /bin/bad\n")

	assert.Equal(t, "1 item needs a fix before this config loads.",
		importFooterLine(res, assert.AnError))
	assert.Equal(t, "1 item carries a # TODO — fix it before you rely on it.",
		importFooterLine(res, nil))

	clean := parseCronForReport(t, "0 3 * * * /bin/good\n")
	assert.Empty(t, importFooterLine(clean, nil), "nothing blocked, nothing to say")
}
