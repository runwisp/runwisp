// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"
)

// TestLiveTOMLDropsOnlyTheUnsafeJob is the property `[daemon] include_cron`
// depends on: a crontab with one job RunWisp can't reproduce still runs the
// others. That is crond's own behaviour with a malformed entry, and it's the
// difference between adopting a crontab and being unable to.
func TestLiveTOMLDropsOnlyTheUnsafeJob(t *testing.T) {
	in := "0 1 * * * /bin/good\n" +
		"99 99 * * * /bin/bad-schedule\n" +
		"0 3 * * * /bin/mail -s r ops%body\n" +
		"0 4 * * * /bin/also-good\n"
	res := parseCron(t, in, CronOptions{})

	live := res.LiveTOML()
	mustContain(t, live, "[tasks.good]")
	mustContain(t, live, "[tasks.also-good]")
	mustNotContain(t, live, "[tasks.bad-schedule]")
	mustNotContain(t, live, "[tasks.mail]")

	// The full render is unchanged: `runwisp import` hands the operator a file to
	// review, so the jobs needing a fix have to be in it.
	full := res.TOML()
	for _, name := range []string{"good", "also-good", "bad-schedule", "mail"} {
		mustContain(t, full, "[tasks."+name+"]")
	}
}

// TestLiveTOMLKeepsABlockingButRunnableJob is the reason blocking and
// unsafe-live are separate axes. A MAILTO blocks the import — nobody gets the
// mail until a notifier exists — but the job itself runs exactly as crond ran
// it. Refusing to schedule it would be the worse answer.
func TestLiveTOMLKeepsABlockingButRunnableJob(t *testing.T) {
	res := parseCron(t, "MAILTO=ops@example.com\n0 * * * * /bin/hourly\n", CronOptions{})
	if got := res.Items()[0].Status(); got != StatusClean {
		t.Fatalf("the MAILTO note belongs to the file, so the row should be clean, got %v", got)
	}
	mustContain(t, res.LiveTOML(), "[tasks.hourly]")

	// And an escaped `%` is a faithful translation, not a loss.
	res = parseCron(t, `0 4 * * * /bin/date +\%Y`+"\n", CronOptions{})
	if got := res.Items()[0].Status(); got != StatusChanged {
		t.Fatalf("status = %v, want changed", got)
	}
	mustContain(t, res.LiveTOML(), "[tasks.date]")
}

// TestLiveTOMLDropsTheEnvChildToo: a task's `[tasks.x.env]` is a separate block,
// so filtering by table name would leave an orphan child table behind and the
// rendered TOML wouldn't even load. block.item is what makes this exact.
func TestLiveTOMLDropsTheEnvChildToo(t *testing.T) {
	in := "PATH=/opt/bin\n99 99 * * * /bin/bad\n"
	live := parseCron(t, in, CronOptions{}).LiveTOML()
	mustNotContain(t, live, "[tasks.bad]")
	mustNotContain(t, live, "[tasks.bad.env]")
	mustNotContain(t, live, "PATH")
}

// TestSkippedLiveNamesEveryDroppedJob: dropping a job without a way to name it
// is the silent failure this whole report exists to prevent, so the loader has
// to be able to say `file:line` for each one.
func TestSkippedLiveNamesEveryDroppedJob(t *testing.T) {
	in := "0 1 * * * /bin/good\n" +
		"99 99 * * * /bin/bad-schedule\n" +
		"not-a-cron-line\n"
	res := parseCron(t, in, CronOptions{})

	var skipped []Item
	for _, it := range res.Items() {
		if !it.LiveEligible() {
			skipped = append(skipped, it)
		}
	}
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skipped jobs, got %+v", skipped)
	}
	if got := []int{skipped[0].Line, skipped[1].Line}; got[0] != 2 || got[1] != 3 {
		t.Errorf("lines = %v, want [2 3]", got)
	}
	for _, it := range skipped {
		if len(it.Notes) == 0 {
			t.Errorf("skipped job at line %d carries no reason", it.Line)
		}
	}
}

// TestItemLineIsTheSourceLine covers the rows that did import: an operator
// staring at a crontab needs the line number, not a derived task name they have
// never seen. Comment and env lines must not shift it.
func TestItemLineIsTheSourceLine(t *testing.T) {
	in := "# a comment\n" +
		"PATH=/opt/bin\n" +
		"\n" +
		"# describes the job\n" +
		"0 1 * * * /bin/first\n" +
		"0 2 * * * /bin/second\n"
	items := parseCron(t, in, CronOptions{}).Items()
	if len(items) != 2 {
		t.Fatalf("expected 2 rows, got %+v", items)
	}
	if items[0].Line != 5 || items[1].Line != 6 {
		t.Errorf("lines = [%d %d], want [5 6]", items[0].Line, items[1].Line)
	}
}

// TestLiveTOMLAccountsForEveryLiveRow is the filtered mirror of
// assertReportAccountsForTOML: the live render and the live-eligible rows must be
// the same set, in both directions. A table with no row is config from nowhere; a
// row with no table is a task the daemon won't have.
func TestLiveTOMLAccountsForEveryLiveRow(t *testing.T) {
	in := "MAILTO=ops@example.com\n" +
		"PATH=/opt/bin\n" +
		"0 1 * * * /bin/good\n" +
		"99 99 * * * /bin/bad\n" +
		"0 2 * * * /bin/mail -s r ops%body\n" +
		"0 3 * * * /bin/good\n" +
		"not-a-cron-line\n"
	res := parseCron(t, in, CronOptions{})
	live := res.LiveTOML()

	inTOML := map[string]bool{}
	for _, line := range strings.Split(live, "\n") {
		if !strings.HasPrefix(line, "[tasks.") {
			continue
		}
		path := strings.Split(strings.Trim(line, "[]"), ".")
		if len(path) == 2 {
			inTOML[path[1]] = true
		}
	}
	inRows := map[string]bool{}
	for _, it := range res.Items() {
		if !it.LiveEligible() {
			if it.Name != "" && inTOML[it.Name] {
				t.Errorf("%q is not live-eligible but LiveTOML defines it", it.Name)
			}
			continue
		}
		inRows[it.Name] = true
		if !inTOML[it.Name] {
			t.Errorf("row %q is live-eligible but LiveTOML defines no table for it", it.Name)
		}
	}
	for name := range inTOML {
		if !inRows[name] {
			t.Errorf("LiveTOML defines %q but no live-eligible row accounts for it", name)
		}
	}
}

// TestUnsafeLiveImpliesBlocking is the pairing rule between the two severity
// axes. They are independent in one direction only: a note can block without
// making the job unrunnable (a MAILTO), but a note saying "the imported form
// isn't what the source would have run" always leaves the operator something to
// fix. A kind marked unsafe-live and non-blocking would drop a job from
// include_cron while `runwisp import` reported it clean — invisible in both
// places at once.
func TestUnsafeLiveImpliesBlocking(t *testing.T) {
	for k := NoteKind(0); k < noteKindCount; k++ {
		slug, blocking, unsafeLive := k.info()
		if slug == "" {
			continue // TestNoteKindsAreTotal owns that failure
		}
		if unsafeLive && !blocking {
			t.Errorf("%s is unsafe to run live but not blocking — the job would be "+
				"dropped from include_cron while the import called it clean", slug)
		}
	}
}

// cronSourceFixture is one crontab as include_cron reads it.
type cronSourceFixture struct {
	body string
	opts CronOptions
}

// namesAcross parses sources in order with one shared Owned, the way
// include_cron folds a glob together, and returns every task name by source.
func namesAcross(t *testing.T, sources ...cronSourceFixture) [][]string {
	t.Helper()
	owned := Owned{}
	out := make([][]string, len(sources))
	for i, src := range sources {
		src.opts.Existing = owned
		for _, it := range parseCron(t, src.body, src.opts).Items() {
			out[i] = append(out[i], it.Name)
			owned[it.Name] = OwnedEntry{Kind: it.Kind}
		}
	}
	return out
}

// TestAddedCrontabLeavesEveryExistingNameAlone is field report #1: dropping a
// file into /etc/cron.d whose job derives the same name must not rename the job
// that already had it, in either direction. Names come from the source, never
// from which file the glob happened to read first.
func TestAddedCrontabLeavesEveryExistingNameAlone(t *testing.T) {
	ops := cronSourceFixture{`15 8 * * 1-5 echo "weekday standup" | logger -t standup` + "\n" +
		"* * * * * echo hi > /tmp/ops.txt\n",
		CronOptions{User: "ops", NameSuffix: "ops", QualifyNames: true}}
	root := cronSourceFixture{"* * * * * echo hi > /tmp/root.txt\n", CronOptions{NameSuffix: "root"}}
	added := cronSourceFixture{"* * * * * www-data cd / && echo hi > /tmp/www2.txt\n",
		CronOptions{System: true, NameSuffix: "aaa", QualifyNames: true}}

	before := namesAcross(t, root, ops)
	after := namesAcross(t, root, added, ops)
	if strings.Join(before[0], ",") != "echo" || strings.Join(before[1], ",") != "logger-ops,echo-ops" {
		t.Fatalf("before = %v", before)
	}
	if strings.Join(after[0], ",") != "echo" || strings.Join(after[2], ",") != "logger-ops,echo-ops" {
		t.Errorf("adding /etc/cron.d/aaa renamed an existing job: before %v, after %v", before, after)
	}
	if strings.Join(after[1], ",") != "echo-aaa" {
		t.Errorf("the new job = %v, want echo-aaa", after[1])
	}
}

// TestBareNameTakenElsewhereGetsTheSourceSuffix: two unqualified sources (root's
// crontab and /etc/crontab) deriving the same name are the leftover a fixed read
// order settles. The later one carries its file's name, and a repeat inside one
// file is numbered by line.
func TestBareNameTakenElsewhereGetsTheSourceSuffix(t *testing.T) {
	got := namesAcross(t,
		cronSourceFixture{"0 1 * * * /bin/backup\n0 2 * * * /bin/backup\n", CronOptions{NameSuffix: "root"}},
		cronSourceFixture{"0 3 * * * root /bin/backup\n", CronOptions{System: true, NameSuffix: "crontab"}},
	)
	if strings.Join(got[0], ",") != "backup,backup-2" || strings.Join(got[1], ",") != "backup-crontab" {
		t.Errorf("names = %v", got)
	}
}

// TestUniqueKeepsPositionalSuffixes pins repeats inside one source: the file's
// own line order is stable, so -2/-3 is too.
func TestUniqueKeepsPositionalSuffixes(t *testing.T) {
	d := newDeduper()
	got := []string{d.unique("job"), d.unique("job"), d.unique("job")}
	want := []string{"job", "job-2", "job-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}
