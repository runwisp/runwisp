// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// deriveCronName builds a readable task name from a command: it finds the first
// real program in command position (skipping wrappers, flags, env assignments,
// and shell builtins with their arguments), then uses that token's basename
// (minus a script extension), sanitized to RunWisp's name rules.
func deriveCronName(command string) string {
	base := firstCommandProgram(strings.Fields(command))
	if base == "" {
		base = "job"
	}
	if ext := filepath.Ext(base); ext != "" && len(ext) <= 5 {
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
// real program — resetting at every `&&`/`||`/`|`/`;`/`(`, stepping over
// redirections and their targets, and skipping env assignments, wrappers, and
// shell builtins together with their arguments. Empty when none match.
//
// The command-position tracking is the whole point: it is what keeps
// `cd / && run-parts …` from being named after `/`, `test -e /run/systemd/system
// || …` after `system`, and `command -v x > /dev/null && x` after `null` — the
// path-like tokens in those lines are arguments to a guard, not the program.
func firstCommandProgram(tokens []string) string {
	atCmd := true       // is the next token the start of a simple command?
	skipTarget := false // is the next token a redirection target to ignore?
	for _, t := range tokens {
		switch {
		case skipTarget:
			skipTarget = false
		case isControlOperator(t):
			atCmd = true
		case isRedirect(t):
			// `> file` leaves its target as the next token; `>file` / `2>&1`
			// carry it inline. Either way it names no program.
			skipTarget = strings.HasSuffix(t, ">") || strings.HasSuffix(t, "<")
		case !atCmd:
			// an argument to the command already found on this line
		case isEnvAssignment(t) || isAllDigits(t) || strings.HasPrefix(t, "-"):
			// leading NAME=value, a flag, or a flag's numeric value — the real
			// program is still ahead, at the same command position.
		case cronWrappers[filepath.Base(t)]:
			// sudo/nice/flock/… — the wrapped program follows it.
		case cronBuiltins[filepath.Base(t)]:
			// cd/test/command/… — a guard whose arguments run to the next operator.
			atCmd = false
		default:
			return filepath.Base(t)
		}
	}
	return ""
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
