// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"path/filepath"
	"regexp"
	"strings"

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
	return cronName(command, cronLauncher)
}

// legacyCronName is the name RunWisp 1.8 and earlier derived for command, before
// PHP console commands were named after their subcommand. A task imported under
// it is still the same job; see namer.resolve.
func legacyCronName(command string) string {
	return cronName(command, func(prog string) (func([]string) string, bool) {
		pick, ok := cronLaunchers[prog]
		return pick, ok
	})
}

func cronName(command string, launcher launcherFunc) string {
	base, launched := firstCommandProgram(strings.Fields(command), launcher)
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
func firstCommandProgram(tokens []string, launcher launcherFunc) (name string, launched bool) {
	atCmd := true       // is the next token the start of a simple command?
	skipTarget := false // is the next token a redirection target to ignore?
	for i, t := range tokens {
		// A `;` glued to a word (`];`, `start;`) ends the simple command just
		// like a free-standing one does.
		word, ends := strings.CutSuffix(t, ";")
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
		case cronWrappers[filepath.Base(word)]:
			// sudo/nice/flock/… — the wrapped program follows it.
		case cronBuiltins[filepath.Base(word)]:
			// cd/test/command/… — a guard whose arguments run to the next operator.
			atCmd = false
		default:
			prog := filepath.Base(word)
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
	return "", false
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
