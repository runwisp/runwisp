// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strconv"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// systemdCalendarShorthands are systemd's OnCalendar= shorthands in the
// normalized form systemd-analyze prints. weekly is Monday, not cron's Sunday.
var systemdCalendarShorthands = map[string]string{
	"minutely":     "*-*-* *:*:00",
	"hourly":       "*-*-* *:00:00",
	"daily":        "*-*-* 00:00:00",
	"weekly":       "Mon *-*-* 00:00:00",
	"monthly":      "*-*-01 00:00:00",
	"quarterly":    "*-01,04,07,10-01 00:00:00",
	"semiannually": "*-01,07-01 00:00:00",
	"yearly":       "*-01-01 00:00:00",
	"annually":     "*-01-01 00:00:00",
}

// systemdWeekdays maps systemd's weekday names, short and long, to cron's
// day-of-week numbers.
var systemdWeekdays = map[string]int{
	"sun": 0, "sunday": 0, "mon": 1, "monday": 1, "tue": 2, "tuesday": 2,
	"wed": 3, "wednesday": 3, "thu": 4, "thursday": 4, "fri": 5, "friday": 5,
	"sat": 6, "saturday": 6,
}

// onCalendarToCron converts a systemd OnCalendar= expression
// ("[DOW] [[Y-]M-D] [H:M[:S]] [TZ]", or a shorthand like "daily") into a
// RunWisp cron spec and the IANA zone it names, if any. The spec has six fields
// only when the seconds field isn't zero. ok is false for anything cron can't
// say: a year other than "*", fractional seconds, or a weekday combined with a
// day of month (systemd requires both, cron fires on either).
func onCalendarToCron(expr string) (spec, tz string, ok bool) {
	tokens, tz := splitCalendarZone(strings.Fields(expr))
	if len(tokens) == 1 {
		if full, ok := systemdCalendarShorthands[strings.ToLower(tokens[0])]; ok {
			tokens = strings.Fields(full)
		}
	}
	dow, date, clock, ok := systemdCalendarParts(tokens)
	if !ok {
		return "", "", false
	}
	month, dom, ok := systemdDate(date)
	if !ok || (dow != "*" && dom != "*") {
		return "", "", false
	}
	sec, minute, hour, ok := systemdClock(clock)
	if !ok {
		return "", "", false
	}
	spec = strings.Join([]string{minute, hour, dom, month, dow}, " ")
	if sec != "0" {
		spec = sec + " " + spec
	}
	return spec, tz, true
}

// splitCalendarZone drops a trailing IANA zone from an OnCalendar= expression's
// tokens and returns it.
func splitCalendarZone(tokens []string) (rest []string, tz string) {
	n := len(tokens)
	if n < 2 || strings.ContainsAny(tokens[n-1], ":*") {
		return tokens, ""
	}
	if _, err := time.LoadLocation(tokens[n-1]); err != nil {
		return tokens, ""
	}
	return tokens[:n-1], tokens[n-1]
}

// systemdCalendarParts sorts an expression's tokens into its weekday (as a cron
// day-of-week), date, and time, filling in systemd's defaults for the omitted
// ones.
func systemdCalendarParts(tokens []string) (dow, date, clock string, ok bool) {
	if len(tokens) == 0 || len(tokens) > 3 {
		return "", "", "", false
	}
	dow, date, clock = "*", "*-*-*", "00:00:00"
	for i, tok := range tokens {
		switch {
		case strings.Contains(tok, ":"):
			clock = tok
		case strings.ContainsAny(tok, "-~"):
			date = tok
		case i == 0:
			if dow, ok = systemdDow(tok); !ok {
				return "", "", "", false
			}
		default:
			return "", "", "", false
		}
	}
	return dow, date, clock, true
}

// systemdClock splits an "H:M[:S]" time into cron's second, minute and hour
// fields.
func systemdClock(clock string) (sec, minute, hour string, ok bool) {
	hms := strings.Split(clock, ":")
	if len(hms) == 2 {
		hms = append(hms, "00")
	}
	if len(hms) != 3 {
		return "", "", "", false
	}
	for i, f := range hms {
		if hms[i], ok = systemdCalendarField(f); !ok {
			return "", "", "", false
		}
	}
	return hms[2], hms[1], hms[0], true
}

// systemdDate splits a "[Y-]M-D" or "[Y-]M~D" date into cron's month and
// day-of-month fields. The year must be "*"; "~N" (the Nth day from the end)
// becomes L / L-(N-1).
func systemdDate(date string) (month, dom string, ok bool) {
	rest, lastDay, fromEnd := strings.Cut(date, "~")
	parts := strings.Split(rest, "-")
	if !fromEnd {
		if len(parts) < 2 {
			return "", "", false
		}
		parts, lastDay = parts[:len(parts)-1], parts[len(parts)-1]
	}
	switch len(parts) {
	case 1:
	case 2:
		if parts[0] != "*" {
			return "", "", false
		}
		parts = parts[1:]
	default:
		return "", "", false
	}
	if month, ok = systemdCalendarField(parts[0]); !ok {
		return "", "", false
	}
	if !fromEnd {
		dom, ok = systemdCalendarField(lastDay)
		return month, dom, ok
	}
	n, err := strconv.Atoi(lastDay)
	switch {
	case err != nil || n < 1 || n > 31:
		return "", "", false
	case n == 1:
		return month, "L", true
	default:
		return month, "L-" + strconv.Itoa(n-1), true
	}
}

// systemdCalendarField converts one numeric calendar component (a comma list of
// "*", "N", "A..B", with an optional "/step") into cron syntax.
func systemdCalendarField(f string) (string, bool) {
	terms := strings.Split(f, ",")
	for i, term := range terms {
		t, ok := systemdCalendarTerm(term)
		if !ok {
			return "", false
		}
		terms[i] = t
	}
	return strings.Join(terms, ","), true
}

// systemdCalendarTerm converts one term of a calendar component: "*", "N",
// "A..B", each with an optional "/step".
func systemdCalendarTerm(term string) (string, bool) {
	base, step, hasStep := strings.Cut(term, "/")
	lo, hi, isRange := strings.Cut(base, "..")
	out := "*"
	if lo != "*" {
		n, ok := systemdNumber(lo)
		if !ok {
			return "", false
		}
		out = n
	} else if isRange {
		return "", false
	}
	if isRange {
		n, ok := systemdNumber(hi)
		if !ok {
			return "", false
		}
		out += "-" + n
	}
	if hasStep {
		n, ok := systemdNumber(step)
		if !ok {
			return "", false
		}
		out += "/" + n
	}
	return out, true
}

// systemdNumber normalizes a non-negative integer ("05" → "5"), rejecting
// anything else, fractional seconds included.
func systemdNumber(s string) (string, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return "", false
	}
	return strconv.Itoa(n), true
}

// systemdDow converts a weekday list ("Mon", "Mon..Fri", "Sat,Sun") into cron
// day-of-week numbers.
func systemdDow(tok string) (string, bool) {
	terms := strings.Split(tok, ",")
	for i, term := range terms {
		lo, hi, isRange := strings.Cut(term, "..")
		a, ok := systemdWeekdays[strings.ToLower(lo)]
		if !ok {
			return "", false
		}
		terms[i] = strconv.Itoa(a)
		if isRange {
			b, ok := systemdWeekdays[strings.ToLower(hi)]
			if !ok {
				return "", false
			}
			terms[i] += "-" + strconv.Itoa(b)
		}
	}
	return strings.Join(terms, ","), true
}

// systemdTimerCosmeticKeys are [Timer] directives with no bearing on when the
// task runs under RunWisp, dropped without a word. Unit is read by the caller.
var systemdTimerCosmeticKeys = map[string]bool{
	"Unit": true, "AccuracySec": true, "WakeSystem": true, "RemainAfterElapse": true,
	"FixedRandomDelay": true, "Persistent": true,
}

// systemdTimerKeys is a [Timer] section sorted by what each directive does.
type systemdTimerKeys struct {
	calendars []string // OnCalendar= values in order, after any empty reset
	interval  string   // the last OnUnitActiveSec=/OnUnitInactiveSec=, as "Key=value"
	boot      string   // the last OnBootSec=/OnStartupSec=, as "Key=value"
	// droppedTriggers are triggers with no RunWisp equivalent; dropped are
	// directives RunWisp doesn't know at all.
	droppedTriggers, dropped []string
}

func readSystemdTimer(timer *systemdSection) systemdTimerKeys {
	var k systemdTimerKeys
	for _, kv := range timer.kvs {
		switch kv.key {
		case "OnCalendar":
			if kv.value == "" {
				k.calendars = nil // an empty assignment resets the list
			} else {
				k.calendars = append(k.calendars, kv.value)
			}
		case "OnUnitActiveSec", "OnUnitInactiveSec":
			k.interval = kv.key + "=" + kv.value
		case "OnBootSec", "OnStartupSec":
			k.boot = kv.key + "=" + kv.value
		case "OnActiveSec", "OnClockChange", "OnTimezoneChange":
			k.droppedTriggers = append(k.droppedTriggers, kv.key)
		case "RandomizedDelaySec":
			// Read by systemdApplyTimer, once there is a cron for jitter to need.
		default:
			if !systemdTimerCosmeticKeys[kv.key] {
				k.dropped = append(k.dropped, kv.key)
			}
		}
	}
	return k
}

// systemdApplyTimer turns a [Timer] section into the task's schedule: cron and
// timezone, catch_up, jitter, and run_on_start. It returns the schedule label
// for the report row.
func systemdApplyTimer(b *block, timer *systemdSection, ref itemRef) string {
	k := readSystemdTimer(timer)
	schedule := ""
	switch {
	case len(k.calendars) > 0:
		schedule = systemdApplyCalendar(b, ref, k.calendars[0])
		if len(k.calendars) > 1 {
			ref.note(NoteSystemdTimerMultiCalendar,
				"the timer has "+strconv.Itoa(len(k.calendars))+" OnCalendar= lines; RunWisp takes one cron, so only "+
					k.calendars[0]+" was imported. Add a task per extra schedule by hand.")
		}
		if k.interval != "" {
			k.droppedTriggers = append(k.droppedTriggers, k.interval)
		}
	case k.interval != "":
		schedule = systemdApplyInterval(b, ref, k.interval)
	}

	if schedule != "" {
		// systemd skips runs missed while the machine was off unless Persistent=
		// is on, and Persistent= only covers OnCalendar.
		if persistent, _ := parseBool(timer.last("Persistent")); !persistent || len(k.calendars) == 0 {
			b.set("catch_up", "0")
		}
		systemdApplyRandomDelay(b, ref, timer.last("RandomizedDelaySec"))
	}
	if k.boot != "" {
		b.set("run_on_start", tomlString(string(model.RunOnStartBoot)))
		ref.note(NoteSystemdTimerApprox,
			k.boot+" → run_on_start = \"boot\": the task runs once per boot, at daemon start, without the delay.")
		if schedule == "" {
			schedule = "@reboot"
		}
	}
	systemdNoteTimerDropped(ref, k)
	return schedule
}

func systemdNoteTimerDropped(ref itemRef, k systemdTimerKeys) {
	if len(k.droppedTriggers) > 0 {
		ref.note(NoteSystemdTimerApprox,
			"these timer triggers have no RunWisp equivalent and were dropped: "+strings.Join(k.droppedTriggers, ", ")+".")
	}
	if len(k.dropped) > 0 {
		ref.note(NoteKeysUnsupported,
			"these [Timer] directives have no RunWisp equivalent and were dropped: "+strings.Join(k.dropped, ", ")+".")
	}
}

// systemdApplyCalendar sets cron (and timezone) from one OnCalendar= value. A
// value that doesn't convert is written as-is under a TODO, the same way an
// unparseable crontab schedule is.
func systemdApplyCalendar(b *block, ref itemRef, calendar string) string {
	spec, tz, ok := onCalendarToCron(calendar)
	var err error
	if ok {
		err = cronspec.Validate(spec, tz)
	}
	if !ok || err != nil {
		b.setComment("cron", tomlString(calendar),
			"TODO: RunWisp couldn't convert this OnCalendar= value, write it as cron.")
		msg := "OnCalendar=" + calendar + " has no cron equivalent RunWisp can write"
		if err != nil {
			msg += ": " + err.Error()
		}
		ref.note(NoteCronUnparseable, msg+".")
		return calendar
	}
	b.set("cron", tomlString(spec))
	if tz != "" {
		b.set("timezone", tomlString(tz))
	}
	return spec
}

// systemdApplyInterval sets an @every cron from OnUnitActiveSec= or
// OnUnitInactiveSec= (passed as "Key=value").
func systemdApplyInterval(b *block, ref itemRef, interval string) string {
	key, value, _ := strings.Cut(interval, "=")
	d, ok := systemdSeconds(value)
	spec := "@every " + d
	if !ok || cronspec.Validate(spec, "") != nil {
		b.setComment("cron", tomlString("@every "+value),
			"TODO: RunWisp couldn't read this interval, write it as a duration like 1h30m.")
		ref.note(NoteCronUnparseable, interval+" isn't an interval RunWisp can read.")
		return "@every " + value
	}
	b.set("cron", tomlString(spec))
	ref.note(NoteSystemdTimerApprox,
		key+"="+value+" → cron = \""+spec+"\": RunWisp counts the interval from the last scheduled run, "+
			"not from when the unit last started or finished.")
	return spec
}

// systemdApplyRandomDelay maps RandomizedDelaySec= onto jitter, the nearest
// RunWisp has to "start up to this much late".
func systemdApplyRandomDelay(b *block, ref itemRef, value string) {
	if value == "" {
		return
	}
	d, ok := systemdSeconds(value)
	switch {
	case !ok:
		ref.note(NoteKeyUnreadable, "RandomizedDelaySec="+value+" isn't a duration RunWisp can read, so it was dropped.")
	case !isZeroDuration(d):
		b.set("jitter", tomlString(d))
		ref.note(NoteSystemdTimerApprox,
			"RandomizedDelaySec="+value+" → jitter = \""+d+"\": RunWisp delays a start only while other "+
				"jittered tasks are running, instead of by a random amount every time.")
	}
}
