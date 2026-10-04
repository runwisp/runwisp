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
	r *strings.Replacer
}

// newSecretRedactor returns a redactor for the given secret values, or nil when
// there is nothing to redact (the common case — a nil *secretRedactor is a safe
// no-op through all its methods, so callers never branch).
func newSecretRedactor(secrets map[string]string) *secretRedactor {
	var values []string
	for _, v := range secrets {
		if v == "" {
			// An empty old string makes strings.Replacer match between every
			// rune; skip it (an empty secret value can't leak anyway).
			continue
		}
		values = append(values, v)
	}
	if len(values) == 0 {
		return nil
	}
	// strings.Replacer tries pairs in argument order at each position, so a
	// secret that is a prefix of another must come after it or the longer one
	// is only partly masked. Map order is random; sort for a fixed result.
	slices.SortFunc(values, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	pairs := make([]string, 0, 2*len(values))
	for _, v := range values {
		pairs = append(pairs, v, redactMask)
	}
	return &secretRedactor{r: strings.NewReplacer(pairs...)}
}

// text returns s with every secret value replaced by the mask.
func (s *secretRedactor) text(t string) string {
	if s == nil {
		return t
	}
	return s.r.Replace(t)
}

// rows returns a redacted copy of a region's rows, leaving the input untouched
// (the terminal renderer keeps ownership of the original slice).
func (s *secretRedactor) rows(in []string) []string {
	if s == nil {
		return in
	}
	out := make([]string, len(in))
	for i, row := range in {
		out[i] = s.r.Replace(row)
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
