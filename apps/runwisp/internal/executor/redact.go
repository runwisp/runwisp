// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"slices"
	"strings"
)

// redactMask is what a matched secret value is rewritten to in captured output.
const redactMask = "[redacted]"

// secretRedactor scrubs known secret values out of a run's captured output
// before it reaches disk, the event bus (SSE / REST), or the station push. It is
// built once per run from the task's resolved [tasks.*.secrets] values, so the
// promise that secret values never leave the daemon holds at the one point
// every downstream consumer reads from.
//
// ponytail: whole-string substring match only. A secret split across two output
// lines, transformed (base64/url-encoded), or only partially printed is not
// caught — this keeps a value that lands verbatim in stdout/stderr out of the
// log, it is not a guarantee against a program that deliberately mangles it.
type secretRedactor struct {
	values []string
}

// newSecretRedactor returns a redactor for the given secret values, or nil when
// there is nothing to redact (the common case — a nil *secretRedactor is a safe
// no-op through all its methods, so callers never branch).
func newSecretRedactor(secrets map[string]string) *secretRedactor {
	var values []string
	for _, v := range secrets {
		if v != "" { // an empty value matches everywhere and can't leak anyway
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return &secretRedactor{values: values}
}

// text returns t with every secret value replaced by the mask. It masks the
// union of every occurrence of every secret, so secrets that overlap (one a
// prefix of another, or one starting inside another) are hidden whole; a
// left-to-right replacer would mask the first and print the rest of the other.
func (s *secretRedactor) text(t string) string {
	if s == nil {
		return t
	}
	var spans [][2]int
	for _, v := range s.values {
		for off := 0; ; {
			i := strings.Index(t[off:], v)
			if i < 0 {
				break
			}
			spans = append(spans, [2]int{off + i, off + i + len(v)})
			off += i + 1
		}
	}
	if len(spans) == 0 {
		return t
	}
	slices.SortFunc(spans, func(a, b [2]int) int { return a[0] - b[0] })
	var out strings.Builder
	last := 0 // end of the text already written or masked
	for i := 0; i < len(spans); {
		start, end := spans[i][0], spans[i][1]
		for i++; i < len(spans) && spans[i][0] < end; i++ {
			end = max(end, spans[i][1])
		}
		out.WriteString(t[last:start])
		out.WriteString(redactMask)
		last = end
	}
	out.WriteString(t[last:])
	return out.String()
}

// rows returns a redacted copy of a region's rows, leaving the input untouched
// (the terminal renderer keeps ownership of the original slice).
func (s *secretRedactor) rows(in []string) []string {
	if s == nil {
		return in
	}
	out := make([]string, len(in))
	for i, row := range in {
		out[i] = s.text(row)
	}
	return out
}

// frames returns a redacted copy of a commit group's frame history.
func (s *secretRedactor) frames(in [][]string) [][]string {
	if s == nil {
		return in
	}
	out := make([][]string, len(in))
	for i, row := range in {
		out[i] = s.rows(row)
	}
	return out
}
