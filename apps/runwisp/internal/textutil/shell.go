// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package textutil

import "strings"

// ShellWord leaves a word the shell reads literally as it is, and
// single-quotes anything else (including the empty word).
func ShellWord(s string) string {
	if s == "" {
		return "''"
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("_-./:=,+@%^", c) >= 0) {
			return ShellQuote(s)
		}
	}
	return s
}

// ShellQuote single-quote-wraps a token for /bin/sh, rendering an embedded
// single quote as close-quote, escaped quote, reopen. Single quotes suppress
// every shell metacharacter, so a value like `'; rm -rf /` becomes an inert
// literal.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// TrimMatchedQuotes strips one matching pair of surrounding single or double
// quotes, the way cron, systemd and env files read a quoted value.
func TrimMatchedQuotes(s string) string {
	if n := len(s); n >= 2 && (s[0] == '"' || s[0] == '\'') && s[n-1] == s[0] {
		return s[1 : n-1]
	}
	return s
}
