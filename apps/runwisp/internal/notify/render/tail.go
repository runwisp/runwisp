// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"strings"

	"github.com/runwisp/runwisp/internal/logutil"
)

// NewOutputTail returns the captured-output tail closure consumed by the
// notify renderer. It reads the last maxLines from the on-disk log file at
// logPath and caps the joined result at maxBytes. Empty/missing logs yield
// "" so the template branch that wraps the tail in <blockquote> / Slack
// code-fence collapses cleanly.
//
// Truncation rules:
//   - empty/whitespace-only lines are skipped (they add no signal but eat
//     budget),
//   - bytes are counted across joined "\n"-separated lines,
//   - when the budget is exceeded mid-line, the cut is taken from the front
//     (keeping the tail of the tail) and prefixed with an ellipsis so the
//     reader knows there's more.
//
// The closure does not HTML-escape its output — the consuming template
// applies tgEscape / jsonEscape before placing the body inside a blockquote
// or code block.
func NewOutputTail() func(logPath string, maxLines, maxBytes int) string {
	return func(logPath string, maxLines, maxBytes int) string {
		if logPath == "" || maxLines <= 0 || maxBytes <= 0 {
			return ""
		}
		lines, _, _, err := logutil.ReadLineRange(logPath, -int64(maxLines), int64(maxLines))
		if err != nil || len(lines) == 0 {
			return ""
		}
		cleaned := cleanTailLines(lines)
		if len(cleaned) == 0 {
			return ""
		}
		return truncateTail(strings.Join(cleaned, "\n"), maxBytes)
	}
}

// cleanTailLines trims trailing whitespace from each record and drops the
// empty/whitespace-only lines (they add no signal but eat budget).
func cleanTailLines(lines []logutil.LogLineRecord) []string {
	cleaned := make([]string, 0, len(lines))
	for _, rec := range lines {
		text := strings.TrimRight(rec.Text, " \t\r\n")
		if text == "" {
			continue
		}
		cleaned = append(cleaned, text)
	}
	return cleaned
}

// truncateTail caps joined at maxBytes. When the budget is exceeded it keeps the
// tail of the tail — the *last* maxBytes-1 bytes — and prefixes a single
// character ellipsis so the reader sees the truncation marker. maxBytes <= 1
// collapses to "…" with nothing else, which is honest enough.
func truncateTail(joined string, maxBytes int) string {
	if len(joined) <= maxBytes {
		return joined
	}
	const ellipsis = "…"
	budget := maxBytes - len(ellipsis)
	if budget <= 0 {
		return ellipsis
	}
	return ellipsis + joined[len(joined)-budget:]
}
