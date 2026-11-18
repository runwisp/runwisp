// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// deriveCronName builds a readable task name from a command: it finds the first
// real program in command position (skipping wrappers, flags, env assignments,
// and shell builtins with their arguments), then uses that token's basename
// (minus a script extension), sanitized to RunWisp's name rules. A launcher
// (`run-parts DIR`, `service NAME`) is named after what it launches instead,
// kept whole with dots and colons as hyphens: `cron.hourly` is a directory, not a file with
// a `.hourly` suffix, and reads better as `cron-hourly` than `cron_hourly`.
func deriveCronName(command string) string {
	return cronName(command, cronLauncher, true)
}

// cronName110 is the name RunWisp 1.9 and 1.10 derived for command, before it
// looked past `sleep`, a piped `echo` and a bare wrapper. Frozen: a task imported
// under it is still the same job; see namer.resolve.
func cronName110(command string) string {
	return cronName(command, cronLauncher, false)
}

// legacyCronName is the name RunWisp 1.8 and earlier derived for command, before
// PHP console commands were named after their subcommand. A task imported under
// it is still the same job; see namer.resolve.
func legacyCronName(command string) string {
	return cronName(command, func(prog string) (func([]string) string, bool) {
		pick, ok := cronLaunchers[prog]
		return pick, ok
	}, false)
}

func cronName(command string, launcher launcherFunc, lookPast bool) string {
	base, launched := firstCommandProgram(strings.Fields(command), launcher, lookPast)
	if base == "" {
		base = "job"
	}
	if launched {
		base = strings.NewReplacer(".", "-", ":", "-").Replace(base)
	} else if ext := filepath.Ext(base); ext != "" && len(ext) <= 5 {
		base = strings.TrimSuffix(base, ext)
	}
	return finalizeTaskName(base, "job")
}

// maxCommentNameWords is the longest comment that still reads as a name.
// Anything longer is a sentence about the job, which stays its description.
const maxCommentNameWords = 4

var nonSlugRun = regexp.MustCompile(`[^a-z0-9]+`)

// commentName turns a short comment above a job (`# nightly backup`) into a
// name (`nightly-backup`). Empty when the comment is too long to be a name or
// has nothing name-like in it.
func commentName(comment string) string {
	if len(strings.Fields(comment)) > maxCommentNameWords {
		return ""
	}
	return strings.Trim(nonSlugRun.ReplaceAllString(strings.ToLower(comment), "-"), "-")
}

// qualifiedName appends a source's suffix to a derived name, so `report` from
// ops's crontab becomes `report-ops`. A name that already says where it came
// from (`e2scrub_all` from /etc/cron.d/e2scrub_all) is left alone.
func qualifiedName(base, suffix string) string {
	if suffix == "" || strings.Contains(base, suffix) {
		return base
	}
	return finalizeTaskName(base+"-"+suffix, base)
}

// finalizeTaskName sanitizes base to RunWisp's name rules, trims stray
// separators, falls back to fallback when nothing usable remains, and caps
// the result at model.TaskNameMaxLength.
func finalizeTaskName(base, fallback string) string {
	base = model.SanitizeTaskName(base)
	base = strings.Trim(base, "_-")
	if base == "" {
		base = fallback
	}
	if len(base) > model.TaskNameMaxLength {
		base = base[:model.TaskNameMaxLength]
	}
	return base
}

// firstCommandProgram walks a command field the way a shell reads it and
// returns the basename of the first token in *command position* that names a
// real program — resetting at every `&&`/`||`/`|`/`;`/`(` and shell keyword,
// stepping over redirections and their targets, and skipping env assignments,
// wrappers, and shell builtins together with their arguments. Empty when none
// match. launched is true when the name came from a launcher's argument rather
// than from the program itself (see cronLaunchers).
//
// The command-position tracking is the whole point: it is what keeps
// `cd / && run-parts …` from being named after `/`, `test -e /run/systemd/system
// || …` after `system`, `command -v x > /dev/null && x` after `null`, and
// `if [ -x … ]; then …` after `if` — the path-like tokens in those lines are
// arguments to a guard, not the program.
//
// lookPast also skips `sleep` like a guard, skips an `echo`/`printf` whose
// output is piped (the program after the `|` is the job), and names a job that
// is only a wrapper or a sleep (`/usr/bin/env > f`) after that word rather than
// nothing. Off only for the frozen older derivations.
func firstCommandProgram(tokens []string, launcher launcherFunc, lookPast bool) (name string, launched bool) {
	atCmd := true       // is the next token the start of a simple command?
	skipTarget := false // is the next token a redirection target to ignore?
	fallback := ""      // the last wrapper or sleep stepped over, for a job that is only that
	for i, t := range tokens {
		// A `;` glued to a word (`];`, `start;`) ends the simple command just
		// like a free-standing one does.
		word, ends := strings.CutSuffix(t, ";")
		prog := filepath.Base(word)
		switch {
		case skipTarget:
			skipTarget = false
		case isControlOperator(t) || shellKeywords[word]:
			atCmd = true
		case isRedirect(word):
			// `> file` leaves its target as the next token; `>file` / `2>&1`
			// carry it inline. Either way it names no program.
			skipTarget = strings.HasSuffix(word, ">") || strings.HasSuffix(word, "<")
		case !atCmd:
			// an argument to the command already found on this line
		case isEnvAssignment(word) || isAllDigits(word) || strings.HasPrefix(word, "-"):
			// leading NAME=value, a flag, or a flag's numeric value — the real
			// program is still ahead, at the same command position.
		case cronWrappers[prog]:
			// sudo/nice/flock/… — the wrapped program follows it.
			fallback = prog
		case cronBuiltins[prog]:
			// cd/test/command/… — a guard whose arguments run to the next operator.
			atCmd = false
		case lookPast && prog == "sleep":
			// `sleep 30 && …` waits; what follows is the job.
			fallback = prog
			atCmd = false
		case lookPast && (prog == "echo" || prog == "printf") && !ends && pipesOn(tokens[i+1:]):
			// `echo "x" | logger …` only feeds the program after the pipe.
			atCmd = false
		default:
			if pick, ok := launcher(prog); ok && !ends {
				if target := pick(commandArgs(tokens[i+1:])); target != "" {
					return filepath.Base(target), true
				}
			}
			return prog, false
		}
		if ends {
			atCmd = true
			skipTarget = false
		}
	}
	if lookPast {
		return fallback, false
	}
	return "", false
}

// pipesOn reports whether the simple command starting at tokens ends in a pipe.
func pipesOn(tokens []string) bool {
	for _, t := range tokens {
		if isControlOperator(t) {
			return t == "|" || t == "|&"
		}
		if strings.HasSuffix(t, ";") {
			return false
		}
	}
	return false
}

// shellKeywords open a fresh command position the way a control operator does:
// the word after `if`, `then`, `!` … is the program, not an argument.
var shellKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"while": true, "until": true, "do": true, "done": true, "!": true,
}

// cronLaunchers are programs that only run something else named by an argument,
// so a job is better named after that argument: `run-parts /etc/cron.daily` is
// the cron.daily job, `invoke-rc.d anacron start` is the anacron job. Each picks
// its target out of the command's non-flag arguments.
var cronLaunchers = map[string]func(args []string) string{
	"run-parts":   lastArg, // run-parts [options] DIRECTORY
	"invoke-rc.d": firstArg,
	"service":     firstArg,
}

// phpProgram matches `php` and versioned binaries such as `php8.3`.
var phpProgram = regexp.MustCompile(`^php[0-9.]*$`)

// phpFrameworkScripts are the console entry points of Laravel (artisan),
// Symfony (bin/console) and CodeIgniter (spark).
var phpFrameworkScripts = map[string]bool{"artisan": true, "console": true, "spark": true}

// launcherFunc looks up the launcher entry for a program, if it is one.
type launcherFunc func(prog string) (pick func(args []string) string, ok bool)

// cronLauncher returns the launcher entry for a program, if it is one.
func cronLauncher(prog string) (func(args []string) string, bool) {
	if phpProgram.MatchString(prog) {
		return phpSubcommand, true
	}
	pick, ok := cronLaunchers[prog]
	return pick, ok
}

// phpSubcommand names `php artisan payments:reconcile` after its subcommand.
// Any other php script gets no target, so it is named after `php` as before.
func phpSubcommand(args []string) string {
	for i, a := range args {
		if phpFrameworkScripts[filepath.Base(a)] {
			return firstArg(args[i+1:])
		}
	}
	return ""
}

var cdPlainDir = regexp.MustCompile(`^cd\s+(~|(?:/|~/)[A-Za-z0-9_./~+@%-]*)\s+&&\s+(\S.*)$`)

// splitLeadingCd turns `cd DIR && rest` into DIR and rest when DIR is a plain
// absolute or ~-relative path with no quoting or shell syntax in it. Anything
// else comes back unchanged with an empty dir. `~user/...` stays in the command:
// working_dir only expands a bare `~` or `~/`, so it would read as relative.
func splitLeadingCd(command string) (dir, rest string) {
	m := cdPlainDir.FindStringSubmatch(strings.TrimSpace(command))
	if m == nil {
		return "", command
	}
	return m[1], m[2]
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func lastArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

// commandArgs returns the non-flag arguments of one simple command: tokens up to
// the next control operator or `;`, minus flags and redirections. A flag's value
// given as its own token (`run-parts -u 022 DIR`) is kept; lastArg still lands
// on the directory, which is all a launcher here needs.
func commandArgs(tokens []string) []string {
	var args []string
	skipTarget := false
	for _, t := range tokens {
		word, ends := strings.CutSuffix(t, ";")
		switch {
		case skipTarget:
			skipTarget = false
		case isControlOperator(t):
			return args
		case isRedirect(word):
			skipTarget = strings.HasSuffix(word, ">") || strings.HasSuffix(word, "<")
		case word != "" && !strings.HasPrefix(word, "-"):
			args = append(args, word)
		}
		if ends {
			return args
		}
	}
	return args
}

// isControlOperator reports whether a token is a shell operator that ends one
// simple command and starts another, so the token after it is a fresh command
// position.
func isControlOperator(t string) bool {
	switch t {
	case "&&", "||", "|", "|&", ";", "&", "(", ")", "{", "}":
		return true
	}
	return false
}

// isRedirect reports whether a token is (or begins with) an I/O redirection
// operator, e.g. `>`, `>>`, `<`, `2>`, `&>`, `>/dev/null`, `2>&1`.
func isRedirect(t string) bool {
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i < len(t) && t[i] == '&' {
		i++
	}
	return i < len(t) && (t[i] == '>' || t[i] == '<')
}

// outputRedirect returns the first file a command sends its stdout or stderr
// to (`>> /var/log/x.log`, `2>/tmp/err`, `&>out`), or "". A duplication like
// `2>&1` names no file, and input (`<`) is not output.
// ponytail: splits on whitespace like the naming does, so a quoted "a > b"
// reads as a redirect too; the note it feeds is only advice.
func outputRedirect(command string) string {
	tokens := strings.Fields(command)
	for i, t := range tokens {
		if !isRedirect(t) || strings.Contains(t, "<") {
			continue
		}
		target := strings.TrimLeft(strings.TrimLeft(t, "0123456789&"), ">")
		if target == "" && i+1 < len(tokens) {
			target = tokens[i+1]
		}
		if target = strings.TrimSuffix(target, ";"); target != "" && !strings.HasPrefix(target, "&") {
			return target
		}
	}
	return ""
}

// devNullTail matches a trailing redirect that sends both stdout and stderr to
// /dev/null: `>/dev/null 2>&1`, `&>/dev/null`, `>/dev/null 2>/dev/null` and
// their `>>`, `1>` and reversed spellings. Stdout alone (`>/dev/null`) is not
// one: cron still mailed stderr, so the crontab asked to hear about errors.
var devNullTail = regexp.MustCompile(`\s+(1?>>? ?/dev/null\s+(?:2>&1|2>>? ?/dev/null)|&>>? ?/dev/null|2>>? ?/dev/null\s+1?>>? ?/dev/null)\s*$`)

// dropDevNullRedirect strips devNullTail from the end of a cron command and
// returns the command and the redirect it dropped, or the command unchanged and
// "". Only the very end counts, so a redirect inside quotes or before a `&` is
// left alone, and it never empties the command. It applies to the last command
// of a list or pipeline only, which is exactly the one the redirect applied to.
func dropDevNullRedirect(command string) (rest, dropped string) {
	loc := devNullTail.FindStringSubmatchIndex(command)
	if loc == nil || strings.TrimSpace(command[:loc[0]]) == "" {
		return command, ""
	}
	return strings.TrimRightFunc(command[:loc[0]], unicode.IsSpace), command[loc[2]:loc[3]]
}

// isAllDigits reports whether s is non-empty and entirely ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isEnvAssignment reports whether a token is a leading NAME=value pair (so the
// name deriver skips `FOO=bar mycmd`).
func isEnvAssignment(token string) bool {
	eq := strings.IndexByte(token, '=')
	if eq <= 0 {
		return false
	}
	slash := strings.IndexByte(token, '/')
	return slash < 0 || slash > eq
}
