// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
)

func TestOnCalendarToCron(t *testing.T) {
	cases := []struct {
		in, spec, tz string
		ok           bool
	}{
		{"minutely", "* * * * *", "", true},
		{"hourly", "0 * * * *", "", true},
		{"daily", "0 0 * * *", "", true},
		{"weekly", "0 0 * * 1", "", true}, // Monday, not cron's Sunday
		{"monthly", "0 0 1 * *", "", true},
		{"quarterly", "0 0 1 1,4,7,10 *", "", true},
		{"semiannually", "0 0 1 1,7 *", "", true},
		{"yearly", "0 0 1 1 *", "", true},
		{"annually", "0 0 1 1 *", "", true},
		{"Mon..Fri *-*-* 09:00", "0 9 * * 1-5", "", true},
		{"*:0/15", "0/15 * * * *", "", true},
		{"*-*-01 04:00:00", "0 4 1 * *", "", true},
		{"*-*~01", "0 0 L * *", "", true},
		{"*-02~03 06:00", "0 6 L-2 2 *", "", true},
		{"Sat,Sun 10:30:15", "15 30 10 * * 6,0", "", true},
		{"*-*-* 02:00 Europe/Bratislava", "0 2 * * *", "Europe/Bratislava", true},
		{"daily UTC", "0 0 * * *", "UTC", true},
		{"Mon,Wed,Fri 08:00", "0 8 * * 1,3,5", "", true},
		{"*-*-* *:*:*", "* * * * * *", "", true},
		{"03-15 12:00", "0 12 15 3 *", "", true},
		{"2027-*-* 00:00", "", "", false},       // a year cron can't say
		{"Mon *-*-01..07 00:00", "", "", false}, // systemd ANDs these, cron ORs
		{"*-*-* 00:00:00.5", "", "", false},
		{"every tuesday", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		spec, tz, ok := onCalendarToCron(tc.in)
		if ok != tc.ok || spec != tc.spec || tz != tc.tz {
			t.Errorf("onCalendarToCron(%q) = %q, %q, %v; want %q, %q, %v", tc.in, spec, tz, ok, tc.spec, tc.tz, tc.ok)
			continue
		}
		if ok {
			if err := cronspec.Validate(spec, tz); err != nil {
				t.Errorf("onCalendarToCron(%q) = %q, which doesn't validate: %v", tc.in, spec, err)
			}
		}
	}
}

// TestSystemdTimerConsumesItsService: passing a timer and the service it starts
// gives one scheduled task, not a task plus a service.
func TestSystemdTimerConsumesItsService(t *testing.T) {
	for _, paths := range [][]string{
		{"testdata/systemd/backup.service", "testdata/systemd/backup.timer"},
		{"testdata/systemd/backup.timer", "testdata/systemd/backup.service"},
	} {
		res, err := ParseSystemdFiles(paths, SystemdOptions{})
		if err != nil {
			t.Fatalf("ParseSystemdFiles: %v", err)
		}
		if tally := res.Tally(); tally.Tasks != 1 || tally.Services != 0 {
			t.Fatalf("%v: got %+v, want 1 task / 0 services", paths, tally)
		}
		out := res.TOML()
		mustContain(t, out, "[tasks.backup]")
		mustNotContain(t, out, "run_on_start")
	}
}

// TestSystemdTimerCatchUp: systemd skips runs missed while the machine was off
// unless Persistent=true, so only that keeps RunWisp's default catch_up.
func TestSystemdTimerCatchUp(t *testing.T) {
	base := "[Service]\nExecStart=/bin/job\n[Timer]\nOnCalendar=daily\n"
	mustContain(t, parseUnit(t, base).TOML(), "catch_up = 0")
	mustContain(t, parseUnit(t, base+"Persistent=false\n").TOML(), "catch_up = 0")
	mustNotContain(t, parseUnit(t, base+"Persistent=true\n").TOML(), "catch_up")
}

// TestSystemdStdinTimerHasNoService: a piped timer has no directory to find its
// service in, so it imports with a blocking note instead of failing.
func TestSystemdStdinTimerHasNoService(t *testing.T) {
	res := parseUnit(t, "[Timer]\nOnCalendar=hourly\n")
	mustContain(t, res.TOML(), `cron = "0 * * * *"`)
	assertNotes(t, res, []NoteKind{NoteSystemdNoName, NoteSystemdTimerNoService})
}

// TestSystemdTimerUnconvertibleCalendar: an OnCalendar= cron can't express is
// written under a TODO, never as a different schedule.
func TestSystemdTimerUnconvertibleCalendar(t *testing.T) {
	res := parseUnit(t, "[Service]\nExecStart=/bin/job\n[Timer]\nOnCalendar=2027-01-01 00:00\n")
	mustContain(t, res.TOML(), `cron = "2027-01-01 00:00"  # TODO`)
	assertNotes(t, res, []NoteKind{NoteSystemdNoName, NoteCronUnparseable})
}
