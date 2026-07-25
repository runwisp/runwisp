// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package importer

import "testing"

// '%' is the one character that means something different to crond than it does
// to the shell RunWisp hands `run` to, so importing a command field verbatim
// imports a command crond never ran. These tests pin both halves of the
// translation and, more importantly, pin that the difference is always visible
// in the report.

func TestSplitCronPercent(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		run       string
		stdin     string
		truncated bool
		unescaped bool
	}{
		{
			name: "no percent at all is left exactly as written",
			in:   "/bin/backup --to /srv",
			run:  "/bin/backup --to /srv",
		},
		{
			name:      "an escaped percent loses the backslash crond would have eaten",
			in:        `/bin/date +\%Y`,
			run:       "/bin/date +%Y",
			unescaped: true,
		},
		{
			name:      "the command ends at the first unescaped percent",
			in:        "/bin/mail ops%the body",
			run:       "/bin/mail ops",
			stdin:     "the body",
			truncated: true,
		},
		{
			name:      "further percents are newlines in the input",
			in:        "/bin/mail ops%one%two%three",
			run:       "/bin/mail ops",
			stdin:     "one\ntwo\nthree",
			truncated: true,
		},
		{
			name:      "a trailing percent carries no input",
			in:        "/bin/rollup%",
			run:       "/bin/rollup",
			truncated: true,
		},
		{
			name:      "escaping applies inside the input too",
			in:        `/bin/mail ops%100\% done`,
			run:       "/bin/mail ops",
			stdin:     "100% done",
			truncated: true,
		},
		{
			// A backslash before anything else is the shell's business, not
			// crond's, so it has to survive untouched.
			name: "a backslash that isn't escaping a percent is left alone",
			in:   `/bin/echo a\tb`,
			run:  `/bin/echo a\tb`,
		},
		{
			name:      "escaped and unescaped percents in one command",
			in:        `/bin/date +\%s%now`,
			run:       "/bin/date +%s",
			stdin:     "now",
			truncated: true,
			unescaped: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitCronPercent(tc.in)
			if got.run != tc.run {
				t.Errorf("run = %q, want %q", got.run, tc.run)
			}
			if got.stdin != tc.stdin {
				t.Errorf("stdin = %q, want %q", got.stdin, tc.stdin)
			}
			if got.truncated != tc.truncated {
				t.Errorf("truncated = %v, want %v", got.truncated, tc.truncated)
			}
			if got.unescaped != tc.unescaped {
				t.Errorf("unescaped = %v, want %v", got.unescaped, tc.unescaped)
			}
		})
	}
}

// TestCronPercentStdinBlocksTheJob is the case RunWisp genuinely cannot express:
// there is no stdin field, so the imported task runs the same command with
// nothing on its input. Importing the raw field instead would run
// `/bin/mail ops%the body` — a command crond never ran, under a clean ✓.
func TestCronPercentStdinBlocksTheJob(t *testing.T) {
	res := parseCron(t, "0 3 * * * /bin/mail -s report ops%the body\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, `run = "/bin/mail -s report ops"  # TODO`)
	mustNotContain(t, out, "%the body")
	if got := res.Items()[0].Status(); got != StatusBlocked {
		t.Fatalf("status = %v, want blocked", got)
	}
	if !hasNoteKind(res, NotePercentStdin) {
		t.Fatalf("expected a stdin note, got %+v", allNotes(res))
	}
	// The input is what the import loses, so it has to appear somewhere the
	// operator will read — naming the loss without naming what was lost leaves
	// them re-reading the crontab.
	mustContain(t, out, "the body")
	assertGeneratedConfigLoads(t, out)
}

// TestCronPercentStdinIsNotInTheDerivedName guards a quieter consequence of
// translating: the name comes from the command, and before the split it came
// from the command *plus* the stdin data.
func TestCronPercentStdinIsNotInTheDerivedName(t *testing.T) {
	res := parseCron(t, "0 3 * * * /bin/rollup%payload\n", CronOptions{})
	if got := res.Items()[0].Name; got != "rollup" {
		t.Fatalf("name = %q, want %q", got, "rollup")
	}
}

// TestCronEscapedPercentIsTranslatedNotBlocked is the common real-world case —
// `date +\%Y` appears in an enormous share of crontabs. It maps exactly, so it
// must not read as a blocker; it is still a change to the text, so it must not
// read as clean either.
func TestCronEscapedPercentIsTranslated(t *testing.T) {
	res := parseCron(t, `0 4 * * * /bin/date +\%Y-\%m >> /var/log/stamp`+"\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, `run = "/bin/date +%Y-%m >> /var/log/stamp"`)
	mustNotContain(t, out, "TODO")
	if got := res.Items()[0].Status(); got != StatusChanged {
		t.Fatalf("status = %v, want changed", got)
	}
	if !hasNoteKind(res, NotePercentTranslated) {
		t.Fatalf("expected a translation note, got %+v", allNotes(res))
	}
	assertGeneratedConfigLoads(t, out)
}

// TestCronPercentFreeCommandStaysClean is the other side: the translation must
// not start editorializing about commands that have no '%' in them.
func TestCronPercentFreeCommandStaysClean(t *testing.T) {
	res := parseCron(t, "0 4 * * * /bin/backup --to /srv\n", CronOptions{})
	if got := res.Items()[0].Status(); got != StatusClean {
		t.Fatalf("status = %v, want clean", got)
	}
}

// TestCronTrailingPercentIsNotABlocker: crond ends the command there and pipes
// nothing, so RunWisp reproduces it exactly by dropping the '%'. Nothing is
// lost, but the text changed, so it is a `~` rather than a `✓`.
func TestCronTrailingPercentIsNotABlocker(t *testing.T) {
	res := parseCron(t, "0 4 * * * /bin/rollup%\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, `run = "/bin/rollup"`)
	mustNotContain(t, out, "TODO")
	if got := res.Items()[0].Status(); got != StatusChanged {
		t.Fatalf("status = %v, want changed", got)
	}
}
