// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"bufio"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// AnacrontabPath is the table anacron reads. On Debian, Ubuntu and Fedora it,
// not /etc/crontab, is what runs /etc/cron.daily, cron.weekly and cron.monthly:
// the /etc/crontab lines for those step aside when anacron is installed.
const AnacrontabPath = "/etc/anacrontab"

// IsAnacrontab reports whether path is in anacrontab format rather than crontab
// format, which is decided by its name the way crond decides a system crontab
// by its directory.
func IsAnacrontab(path string) bool {
	return filepath.Base(path) == "anacrontab"
}

// AnacronOwnsPath reports whether a live anacron reads this file itself. The
// anacrontab counterpart of CronOwnsPath, for the same hold decision.
func AnacronOwnsPath(path string) bool {
	return path != "" && filepath.Clean(path) == AnacrontabPath
}

// AnacronUnits are the systemd units that run anacron: the hourly timer and the
// service it starts (which is also enabled to run once at boot). Like CronUnits,
// one list for both the hold probe and the take-over mask.
func AnacronUnits() []string {
	return []string{"anacron.timer", "anacron.service"}
}

// anacronDefaultHour is when an anacron job lands without START_HOURS_RANGE.
// Debian's anacron.timer first fires at 07:30, so 07:00 + the job's delay is
// about when anacron would have started it on a machine that was on.
const anacronDefaultHour = 7

// ParseAnacrontab converts an anacrontab into a *Result, the same shape
// ParseCrontab produces, so include_cron and `runwisp import cron` treat both
// alike. Each `period delay job-id command` line becomes a cron task named
// after its job-id:
//
//   - the period becomes a schedule at START_HOURS_RANGE's first hour (07:00
//     without one) plus the delay in minutes: daily, Mondays for 7 days, the 1st
//     for @monthly, January 1st for @yearly;
//   - catch_up = 1, which is anacron's whole point: a machine that was off when
//     the job was due runs it once when it comes back.
//
// Jobs run as opts.User: root for the system /etc/anacrontab, the caller's own
// account for a user anacrontab (`anacron -t`). RANDOM_DELAY is not carried over.
func ParseAnacrontab(r io.Reader, opts CronOptions) (*Result, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	// An anacrontab has no user column and nothing to sniff for one.
	opts.System, opts.Detect = false, false
	res := &Result{}
	cp := &crontabParser{
		res:     res,
		names:   newNamer(res, opts.Existing, opts.NameSuffix),
		opts:    opts,
		env:     map[string]string{},
		anacron: true,
	}
	hour := anacronStartHour(string(data))

	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		cp.line = line
		cp.feedAnacronLine(sc.Text(), hour)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return cp.res, nil
}

// feedAnacronLine routes one anacrontab line: comments and environment lines
// mean what they mean in a crontab, anacron's own two settings are consumed,
// and a job line is rewritten into the crontab job it schedules like.
func (cp *crontabParser) feedAnacronLine(raw string, hour int) {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		cp.feedLine(raw)
		return
	}
	if name, _, ok := cronEnvLine(line); ok {
		// START_HOURS_RANGE and RANDOM_DELAY are anacron's own settings, not the
		// jobs' environment.
		if name != "START_HOURS_RANGE" && name != "RANDOM_DELAY" {
			cp.feedLine(raw)
		}
		return
	}
	fields, command, ok := splitFields(line, 3)
	schedule, exact := anacronSchedule(fields, hour)
	if !ok || command == "" || schedule == "" || strings.Contains(fields[2], "/") {
		cp.addItem(line).note(NoteLineUnparseable,
			"this isn't `period delay job-id command`, so nothing was imported for it.")
		cp.pendingComment = ""
		return
	}
	cp.jobName = fields[2]
	cp.approxPeriod = !exact
	cp.handleJob(schedule + " " + command)
	cp.jobName, cp.approxPeriod = "", false
}

// anacronSchedule maps an anacron period and delay onto a cron expression.
// exact is false for an N-day period other than 1 and 7, which cron can only
// approximate as every Nth day of the month.
func anacronSchedule(fields []string, hour int) (schedule string, exact bool) {
	if len(fields) < 3 {
		return "", false
	}
	delay, err := strconv.Atoi(fields[1])
	if err != nil || delay < 0 {
		return "", false
	}
	at := strconv.Itoa(delay%60) + " " + strconv.Itoa((hour+delay/60)%24)
	switch fields[0] {
	case "1", "@daily":
		return at + " * * *", true
	case "7", "@weekly":
		return at + " * * 1", true
	case "@monthly":
		return at + " 1 * *", true
	case "@yearly", "@annually":
		return at + " 1 1 *", true
	}
	days, err := strconv.Atoi(fields[0])
	if err != nil || days < 1 {
		return "", false
	}
	return at + " */" + strconv.Itoa(days) + " * *", false
}

// anacronStartHour is the first hour of START_HOURS_RANGE (e.g. `3-22` on
// Fedora), the earliest anacron starts anything, or anacronDefaultHour.
func anacronStartHour(data string) int {
	for _, raw := range strings.Split(data, "\n") {
		name, value, ok := cronEnvLine(strings.TrimSpace(raw))
		if !ok || name != "START_HOURS_RANGE" {
			continue
		}
		start, _, _ := strings.Cut(value, "-")
		if h, err := strconv.Atoi(strings.TrimSpace(start)); err == nil && h >= 0 && h < 24 {
			return h
		}
	}
	return anacronDefaultHour
}
