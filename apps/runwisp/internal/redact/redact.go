// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package redact provides a concurrency-safe value-redactor used as the
// content backstop that keeps resolved secrets out of API output and captured
// run logs. It holds the set of hidden resolved values (secrets pulled from
// ${VAR}/${file:...} placeholders that are not revealed) and masks any
// occurrence of them by content, regardless of which field or stream carried
// them. Forgetting to redact a specific field is therefore safe: the value is
// still masked by the backstop.
package redact

import (
	"strings"
	"sync"
)

// Placeholder is the literal substituted in place of a redacted value. It
// matches internal/notify's per-message redaction so masked output reads the
// same everywhere.
const Placeholder = "[redacted]"

// minRedactLen is the shortest value the redactor will mask. Masking very
// short strings would mangle unrelated output (e.g. a 1-2 char secret matching
// common substrings), so values below the floor are ignored — a credential
// that short isn't worth protecting at the cost of pathological over-redaction.
const minRedactLen = 4

// Redactor masks known secret values in arbitrary text. The zero value is not
// usable; call New. A nil *Redactor is safe to call and acts as a no-op so
// callers can stay unconditional.
type Redactor struct {
	mu       sync.Mutex
	values   map[string]struct{}
	replacer *strings.Replacer
}

// New returns an empty Redactor.
func New() *Redactor {
	return &Redactor{values: make(map[string]struct{})}
}

// Add registers value as a secret to mask. Values shorter than the floor are
// ignored. Adding is idempotent; the underlying matcher is rebuilt only when
// the set actually grows. Safe for concurrent use and for late additions (e.g.
// the executor registering a file-sourced secret resolved at spawn time).
func (r *Redactor) Add(value string) {
	if r == nil || len(value) < minRedactLen {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.values[value]; ok {
		return
	}
	r.values[value] = struct{}{}
	pairs := make([]string, 0, len(r.values)*2)
	for v := range r.values {
		pairs = append(pairs, v, Placeholder)
	}
	r.replacer = strings.NewReplacer(pairs...)
}

// Redact replaces every occurrence of every registered secret in s with the
// placeholder. A nil or empty Redactor returns s unchanged.
func (r *Redactor) Redact(s string) string {
	if r == nil {
		return s
	}
	r.mu.Lock()
	rep := r.replacer
	r.mu.Unlock()
	if rep == nil {
		return s
	}
	return rep.Replace(s)
}
