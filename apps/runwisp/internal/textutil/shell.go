// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package textutil

import "strings"

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
