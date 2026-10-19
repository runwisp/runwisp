// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"io/fs"
	"strings"
)

// noopGuard reports whether a crontab command is a distro guard that skips its
// job on this host, and why. Debian and Ubuntu ship cron lines that hand their
// work to something else when it is present:
//
//	test -x /usr/sbin/anacron || ( cd / && run-parts --report /etc/cron.daily )
//	test -e /run/systemd/system || SERVICE_MODE=1 /sbin/e2scrub_all -A -r
//	if [ -x /etc/init.d/anacron ] && ! [ -d /run/systemd/system ]; then …; fi
//
// On a systemd box with anacron, every one of those runs, does nothing, and
// exits 0. Importing them fills the task list with jobs that can never do
// anything, which buries the ones that do.
//
// stat answers the file tests; it is the host's os.Stat for this machine's own
// crontabs, and nil means "not this machine's", where no guard is evaluated.
//
// ponytail: recognizes only the two stock shapes above (a leading `test`/`[`
// guard before `||`, and `if <tests joined by &&>; then … fi`) with -e/-d/-f/-x.
// Anything else is imported as written. Add shapes when a distro ships one.
func noopGuard(command string, stat func(string) (fs.FileInfo, error)) (reason string, noop bool) {
	if stat == nil {
		return "", false
	}
	tokens := strings.Fields(command)
	if len(tokens) > 0 && tokens[0] == "if" {
		return ifGuard(tokens[1:], stat)
	}
	t, next, ok := parseFileTest(tokens)
	if !ok || next >= len(tokens) || tokens[next] != "||" {
		return "", false
	}
	if holds := t.eval(stat); holds {
		return t.describe(holds), true
	}
	return "", false
}

// ifGuard handles `if T1 && T2 …; then …; fi`: the body never runs when any
// test in the condition is false.
func ifGuard(tokens []string, stat func(string) (fs.FileInfo, error)) (string, bool) {
	for {
		t, next, ok := parseFileTest(tokens)
		if !ok {
			return "", false
		}
		if holds := t.eval(stat); holds == t.negated {
			return t.describe(holds), true
		}
		if t.ended {
			return "", false // the whole condition holds, so the body runs
		}
		if next >= len(tokens) || tokens[next] != "&&" {
			return "", false
		}
		tokens = tokens[next+1:]
	}
}

// fileTest is one `[ -op path ]` / `test -op path`, optionally `!`-negated.
type fileTest struct {
	op, path string
	negated  bool
	// ended is set when the test's last token carried a `;`, which closes an
	// if-condition.
	ended bool
}

// parseFileTest reads one file test from the front of tokens and returns the
// index of the token after it.
func parseFileTest(tokens []string) (t fileTest, next int, ok bool) {
	i := 0
	if i < len(tokens) && tokens[i] == "!" {
		t.negated = true
		i++
	}
	rest := tokens[i:]
	switch {
	case len(rest) >= 3 && rest[0] == "test":
		t.op = rest[1]
		t.path, t.ended = strings.CutSuffix(rest[2], ";")
		next = i + 3
	case len(rest) >= 4 && rest[0] == "[":
		bracket, ended := strings.CutSuffix(rest[3], ";")
		if bracket != "]" {
			return t, 0, false
		}
		t.op, t.path, t.ended = rest[1], rest[2], ended
		next = i + 4
	default:
		return t, 0, false
	}
	switch t.op {
	case "-e", "-d", "-f", "-x":
	default:
		return t, 0, false
	}
	if !strings.HasPrefix(t.path, "/") {
		return t, 0, false // a relative or quoted path depends on more than this host
	}
	return t, next, true
}

// eval reports whether the test, ignoring negation, is true on this host.
func (t fileTest) eval(stat func(string) (fs.FileInfo, error)) bool {
	info, err := stat(t.path)
	if err != nil {
		return false
	}
	switch t.op {
	case "-d":
		return info.IsDir()
	case "-f":
		return info.Mode().IsRegular()
	case "-x":
		return info.Mode()&0o111 != 0
	}
	return true
}

// describe says what the host looks like, given the un-negated result.
func (t fileTest) describe(holds bool) string {
	what := map[string][2]string{
		"-e": {"exists", "does not exist"},
		"-d": {"is a directory", "is not a directory"},
		"-f": {"is a file", "is not a file"},
		"-x": {"is executable", "is not executable"},
	}[t.op]
	if holds {
		return t.path + " " + what[0]
	}
	return t.path + " " + what[1]
}
