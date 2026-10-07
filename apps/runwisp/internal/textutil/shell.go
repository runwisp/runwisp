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
