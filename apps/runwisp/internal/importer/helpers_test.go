// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"
)

func TestIsAllDigits(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     true,
		"12345": true,
		"1a2":   false,
		"-1":    false,
		" 1":    false,
	}
	for in, want := range cases {
		if got := isAllDigits(in); got != want {
			t.Errorf("isAllDigits(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsEnvAssignment(t *testing.T) {
	cases := map[string]bool{
		"FOO=bar":    true,
		"FOO=":       true,
		"=bar":       false, // no name
		"plaincmd":   false,
		"/usr/bin/x": false,
		"A=b/c":      true,  // '=' before any '/'
		"/opt/a=b":   false, // '/' before '=' → a path, not an assignment
	}
	for in, want := range cases {
		if got := isEnvAssignment(in); got != want {
			t.Errorf("isEnvAssignment(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseBool(t *testing.T) {
	truthy := []string{"true", "1", "yes", "on", "TRUE", " On "}
	for _, v := range truthy {
		if b, ok := parseBool(v); !ok || !b {
			t.Errorf("parseBool(%q) = (%v, %v), want (true, true)", v, b, ok)
		}
	}
	falsy := []string{"false", "0", "no", "off"}
	for _, v := range falsy {
		if b, ok := parseBool(v); !ok || b {
			t.Errorf("parseBool(%q) = (%v, %v), want (false, true)", v, b, ok)
		}
	}
	if _, ok := parseBool("maybe"); ok {
		t.Error("parseBool(maybe) should report not-ok")
	}
}

// TestNoteKindsAreTotal walks the enum so that adding a kind without deciding
// its severity is a red test rather than a note that silently reads as harmless.
func TestNoteKindsAreTotal(t *testing.T) {
	for k := NoteKind(0); k < noteKindCount; k++ {
		if k.Slug() == "" {
			t.Errorf("NoteKind(%d) has no entry in info() — decide its slug and whether it blocks", int(k))
		}
	}
}

// TestNoteSlugsAreUnique keeps the slugs usable as identities in the report
// goldens: two kinds sharing one slug would make a golden ambiguous.
func TestNoteSlugsAreUnique(t *testing.T) {
	seen := map[string]NoteKind{}
	for k := NoteKind(0); k < noteKindCount; k++ {
		slug := k.Slug()
		if prev, dup := seen[slug]; dup {
			t.Errorf("slug %q is used by both NoteKind(%d) and NoteKind(%d)", slug, int(prev), int(k))
		}
		seen[slug] = k
	}
}

// TestDeriveStatusRanksBlockingAboveSkipped pins the ordering that makes an
// [eventlistener] a "!" and not a "-". Both emit nothing, but one leaves the
// operator work to do and the other leaves them nothing — collapsing the two
// would hide the case that needs a human. See deriveStatus.
func TestDeriveStatusRanksBlockingAboveSkipped(t *testing.T) {
	blocking := Item{Source: "memmon", Notes: []Note{{Kind: NoteSectionUnsupported}}}
	if got := deriveStatus(blocking); got != StatusBlocked {
		t.Errorf("a job that emitted nothing but needs reimplementing = %v, want blocked", got)
	}
	skipped := Item{Source: "backup", Notes: []Note{{Kind: NoteAlreadyDefined}}}
	if got := deriveStatus(skipped); got != StatusSkipped {
		t.Errorf("a job the live config already owns = %v, want skipped", got)
	}
}

// TestZeroItemIsNotClean is why Status is a method and not a field. As a field
// its zero value was StatusClean, so any Item that reached a renderer without
// being stamped rendered a green ✓ — the safest-looking mark as the default,
// which is precisely the silent-success this report was built to remove. Derived,
// an unfilled row reads as "nothing was imported for this", which is true.
func TestZeroItemIsNotClean(t *testing.T) {
	if (Item{}).Status() == StatusClean {
		t.Error("an Item nobody filled in must not read as cleanly imported")
	}
}

// TestDeriveStatusMakesADifferenceVisible is gap 4: a job that lost a setting
// used to render with the same green mark as one that mapped perfectly, because
// the note's severity and the row's mark were decided in different places.
func TestDeriveStatusMakesADifferenceVisible(t *testing.T) {
	clean := Item{Name: "web", Kind: "service", Schedule: "service", Run: "/usr/bin/web"}
	if got := deriveStatus(clean); got != StatusClean {
		t.Errorf("nothing lost = %v, want clean", got)
	}
	lost := clean
	lost.Notes = []Note{{Kind: NoteLogsDropped}}
	if got := deriveStatus(lost); got != StatusChanged {
		t.Errorf("a dropped setting = %v, want changed", got)
	}
}

// TestTallyCoversEveryRow is the arithmetic form of "every job gets exactly one
// row": the four marks partition the rows, so they must add up to the total.
func TestTallyCoversEveryRow(t *testing.T) {
	in := "0 1 * * * /bin/job\n0 2 * * * /bin/job\n99 99 * * * /bin/bad\nnot-a-cron-line\n"
	res := parseCron(t, in, CronOptions{Existing: Owned{"rollup": {Kind: "task", Run: "/bin/rollup"}}})
	tally := res.Tally()
	if got, want := tally.Total(), len(res.Items()); got != want {
		t.Errorf("marks account for %d rows, but there are %d", got, want)
	}
	if tally.Total() != 4 {
		t.Errorf("expected a row per job line, got %+v", tally)
	}
}

// TestFileLevelNotesGetNoRow pins the row-vs-file-note rule: a note about the
// file's structure must not invent a job. MAILTO is a crontab setting, not a
// job, so it belongs at the bottom — and the one job line gets the only row.
func TestFileLevelNotesGetNoRow(t *testing.T) {
	res := parseCron(t, "MAILTO=ops@example.com\n0 * * * * /bin/hourly\n", CronOptions{})
	if got := len(res.Items()); got != 1 {
		t.Errorf("expected 1 row for 1 job line, got %d: %+v", got, res.Items())
	}
	if len(res.Notes()) != 1 {
		t.Errorf("expected the MAILTO note at file level, got %+v", res.Notes())
	}
	for _, it := range res.Items() {
		for _, n := range it.Notes {
			if n.Kind == NoteMailto {
				t.Error("MAILTO is about the file, not about any one job")
			}
		}
	}
}

// TestItemRunIsVerbatim locks the deletion of the old byte-slicing truncate:
// this package hands the CLI the whole command and lets it decide about width.
func TestItemRunIsVerbatim(t *testing.T) {
	long := "/usr/bin/reap --path /tmp " + strings.Repeat("--flag ", 50) + ">> /var/log/reap.log 2>&1"
	res := parseCron(t, "0 4 * * * "+long+"\n", CronOptions{})
	if got := res.Items()[0].Run; got != long {
		t.Errorf("Run was not verbatim:\n got %q\nwant %q", got, long)
	}
}

// TestUnparseableLineGetsARow is gap 1 at its sharpest: a line the parser
// couldn't read is still a job the operator wrote, so it gets a row carrying the
// line untruncated rather than a note at the bottom of the report.
func TestUnparseableLineGetsARow(t *testing.T) {
	// Fewer than five fields, so there is no schedule to read — and longer than
	// the 60 bytes the old code truncated at.
	line := "/usr/bin/a-command-whose-name-is-long-enough-to-have-been-truncated --flag"
	res := parseCron(t, line+"\n", CronOptions{})
	items := res.Items()
	if len(items) != 1 {
		t.Fatalf("expected one row, got %+v", items)
	}
	if items[0].Source != line {
		t.Errorf("Source = %q, want the line verbatim", items[0].Source)
	}
	if items[0].Status() != StatusBlocked {
		t.Errorf("status = %v, want blocked", items[0].Status())
	}
	if len(res.Notes()) != 0 {
		t.Errorf("the unreadable line belongs on its row, not at file level: %+v", res.Notes())
	}
}

// TestTomlStringEscapesSubstitution pins the difference between the two
// formatters. tomlString is the default and escapes `${` because config.Load
// substitutes it; tomlVerbatimString is the opt-out for the expand:"-" fields.
func TestTomlStringEscapesSubstitution(t *testing.T) {
	if got, want := tomlString("nightly ${DB} dump"), `"nightly $${DB} dump"`; got != want {
		t.Errorf("tomlString = %s, want %s", got, want)
	}
	if got, want := tomlVerbatimString("nightly ${DB} dump"), `"nightly ${DB} dump"`; got != want {
		t.Errorf("tomlVerbatimString = %s, want %s", got, want)
	}
	// A lone `$` passes through the expander untouched, so it must not be escaped
	// either — `awk '{print $1}'` in a description is common and harmless.
	if got, want := tomlString("print $1"), `"print $1"`; got != want {
		t.Errorf("tomlString = %s, want %s", got, want)
	}
	// An already-escaped-looking `$${` in the source is still literal text to
	// cron, so it needs escaping too or it would come back as `${`.
	if got, want := tomlString("$${X}"), `"$$${X}"`; got != want {
		t.Errorf("tomlString = %s, want %s", got, want)
	}
}

func TestTomlStringMultiline(t *testing.T) {
	// A single-line value is a basic quoted string.
	if got := tomlVerbatimString("echo hi"); got != `"echo hi"` {
		t.Errorf("tomlVerbatimString single-line = %q", got)
	}
	// A multi-line value becomes a multi-line basic string with escaped
	// backslashes and a guarded embedded triple-quote.
	got := tomlVerbatimString("line1\n\\path\n\"\"\"oops")
	if !strings.HasPrefix(got, "\"\"\"\n") || !strings.HasSuffix(got, "\"\"\"") {
		t.Errorf("multi-line tomlVerbatimString missing triple-quote fences: %q", got)
	}
	if strings.Contains(got, `\path`) == false {
		t.Errorf("backslash should be escaped (doubled): %q", got)
	}
}

func TestServiceOnlyTaskDropsWithNote(t *testing.T) {
	sd := newSupervisordState(SupervisordOptions{})
	ref := sd.res.addItem("web")
	// On a service the key applies and no note is added.
	if !sd.serviceOnly("priority", ref, true) {
		t.Error("serviceOnly should return true for a service")
	}
	if notes := sd.res.items[0].Notes; len(notes) != 0 {
		t.Errorf("no note expected for a service, got %v", notes)
	}
	// On a run-once task it returns false and records why the key was dropped.
	if sd.serviceOnly("priority", ref, false) {
		t.Error("serviceOnly should return false for a task")
	}
	notes := sd.res.items[0].Notes
	if len(notes) != 1 || !strings.Contains(notes[0].Message, "run-once task") {
		t.Errorf("expected a dropped-key note, got %v", notes)
	}
}
