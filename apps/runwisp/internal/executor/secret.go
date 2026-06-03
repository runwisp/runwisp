// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/redact"
	"github.com/runwisp/runwisp/internal/secretref"
)

// SecretResolver resolves ${...} placeholders in task-declared values at spawn
// time — inline env values and the shell script. Config keeps these fields raw
// (so the API/UI/logs never carry a resolved secret), and this is the seam
// where the daemon turns them into the real values the process needs.
//
// File-sourced secrets resolved here are also added to the Redactor so they are
// masked in captured output even if the backing file changed since boot. Env
// placeholders (${VAR}) were already classified and seeded at boot — env does
// not change over a daemon's lifetime — so only ${file:...} refs are re-added.
//
// A nil *SecretResolver resolves nothing (returns inputs unchanged); backends
// constructed without one — as in unit tests — behave as if no placeholders
// were present.
type SecretResolver struct {
	DataDir  string
	Redactor *redact.Redactor
}

// value resolves a single placeholder-bearing string.
func (r *SecretResolver) value(s string) (string, error) {
	if r == nil || !secretref.Contains(s) {
		return s, nil
	}
	resolved, refs, err := secretref.Resolve(s, r.DataDir)
	if err != nil {
		return "", err
	}
	// Re-seed file contents read at spawn: a ${file:...} may have changed since
	// boot, so the boot-time value in the Redactor could be stale. We add the
	// exact resolved substring, not the whole string, so only the secret is
	// masked. Env refs were seeded (reveal-aware) at boot and don't change.
	for _, ref := range refs {
		if ref.FromFile {
			r.Redactor.Add(ref.Value)
		}
	}
	return resolved, nil
}

// envMap returns a copy of in with every value resolved. It returns in
// unchanged when there is nothing to resolve so the executor keeps its
// "no env overlay → inherit parent" fast path.
func (r *SecretResolver) envMap(in map[string]string) (map[string]string, error) {
	if r == nil || len(in) == 0 {
		return in, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		resolved, err := r.value(v)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}
