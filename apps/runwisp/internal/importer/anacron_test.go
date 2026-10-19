// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"
)

// stockDebianAnacrontab is /etc/anacrontab as Debian and Ubuntu ship it. On a
// box with anacron it, not /etc/crontab, runs cron.daily/weekly/monthly, so a
// takeover that never read it left those jobs out of RunWisp entirely.
const stockDebianAnacrontab = `# /etc/anacrontab: configuration file for anacron

SHELL=/bin/sh
HOME=/root
LOGNAME=root

# These replace cron's entries
1	5	cron.daily	run-parts --report /etc/cron.daily
7	10	cron.weekly	run-parts --report /etc/cron.weekly
@monthly	15	cron.monthly	run-parts --report /etc/cron.monthly
@yearly	20	cron.yearly	run-parts --report /etc/cron.yearly
`

func parseAnacron(t *testing.T, in string) *Result {
	t.Helper()
	res, err := ParseAnacrontab(strings.NewReader(in), CronOptions{User: "root"})
	if err != nil {
		t.Fatalf("ParseAnacrontab: %v", err)
	}
	return res
}

func TestAnacrontabStockDebian(t *testing.T) {
	res := parseAnacron(t, stockDebianAnacrontab)
	out := res.TOML()

	for name, cron := range map[string]string{
		"cron-daily":   "5 7 * * *",
		"cron-weekly":  "10 7 * * 1",
		"cron-monthly": "15 7 1 * *",
		"cron-yearly":  "20 7 1 1 *",
	} {
		mustContain(t, out, "[tasks."+name+"]")
		mustContain(t, out, `cron = "`+cron+`"`)
	}
	mustContain(t, out, `run = "run-parts --report /etc/cron.daily"`)
	mustContain(t, out, `user = "root"`)
	mustContain(t, out, `shell = "/bin/sh"`)
	mustContain(t, out, "catch_up = 1")
	mustNotContain(t, out, "catch_up = 0")
	if tally := res.Tally(); tally.Tasks != 4 || tally.Blocked != 0 {
		t.Fatalf("tally = %+v, want 4 clean tasks: %+v", tally, allNotes(res))
	}
	for _, it := range res.Items() {
		if !it.LiveEligible() {
			t.Errorf("%s must be schedulable live: %+v", it.Name, it.Notes)
		}
	}
}

// TestAnacrontabFedora covers cronie-anacron's table: START_HOURS_RANGE moves
// the jobs, RANDOM_DELAY is anacron's setting and never reaches a job's env,
// and a delay past the hour rolls over.
func TestAnacrontabFedora(t *testing.T) {
	res := parseAnacron(t, "SHELL=/bin/sh\nPATH=/sbin:/bin:/usr/sbin:/usr/bin\nMAILTO=root\n"+
		"RANDOM_DELAY=45\nSTART_HOURS_RANGE=3-22\n\n"+
		"1	5	cron.daily		nice run-parts /etc/cron.daily\n"+
		"7	75	cron.weekly		nice run-parts /etc/cron.weekly\n")
	out := res.TOML()
	mustContain(t, out, "[tasks.cron-daily]\ncron = \"5 3 * * *\"")
	mustContain(t, out, "[tasks.cron-weekly]\ncron = \"15 4 * * 1\"")
	mustContain(t, out, `PATH = "/sbin:/bin:/usr/sbin:/usr/bin"`)
	mustNotContain(t, out, "RANDOM_DELAY")
	mustNotContain(t, out, "START_HOURS_RANGE")
}

func TestAnacrontabOddPeriodAndBadLines(t *testing.T) {
	res := parseAnacron(t, "3	0	every-3	/usr/bin/sweep\nnot a job line\n1	x	bad-delay	/bin/true\n")
	out := res.TOML()
	mustContain(t, out, `cron = "0 7 */3 * *"`)
	notes := allNotes(res)
	var approx, unparseable int
	for _, n := range notes {
		switch n.Kind {
		case NoteAnacronPeriod:
			approx++
		case NoteLineUnparseable:
			unparseable++
		}
	}
	if approx != 1 || unparseable != 2 {
		t.Fatalf("want 1 period note and 2 unparseable lines, got %+v", notes)
	}
}
