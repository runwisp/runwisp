// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// FailedExecutionReasons are the end reasons that represent a run which actually
// executed (or attempted to start) and failed — the only reasons where
// automatically re-running the command is meaningful. It is the single source of
// truth for auto retry/restart eligibility (runtime/retry.IsFailedExecution) and
// it seeds the default failure classification (DefaultFailureTokens).
//
// It is deliberately narrower than Run.IsRetryable (the operator-facing "may I
// re-run this?" affordance, which also covers stopped/timeout/etc.) and fully
// separate from the user-configurable failure set: promoting `stopped` to a
// failure must never make the daemon re-run a run the operator just stopped.
var FailedExecutionReasons = []EndReason{
	ReasonFailed, ReasonTimeout, ReasonCrashed, ReasonLogOverflow, ReasonStartFailed,
}

// DefaultFailureTokens is the built-in `failures` list used when a task (and
// [defaults]) leave the key unset: every failed-execution reason plus a missed
// scheduled run. Reason tokens only — no exit ranges (the `failed` reason
// already covers every non-zero exit by default).
var DefaultFailureTokens = func() []string {
	out := make([]string, 0, len(FailedExecutionReasons)+1)
	for _, r := range FailedExecutionReasons {
		out = append(out, string(r))
	}
	return append(out, string(ReasonMissed))
}()

// defaultFailureReasons is DefaultFailureTokens parsed once, used as the
// fallback for a Task built outside the config loader (tests, ad-hoc dispatch)
// whose FailureReasons map is still nil.
var defaultFailureReasons = func() map[EndReason]struct{} {
	m := make(map[EndReason]struct{}, len(FailedExecutionReasons)+1)
	for _, r := range FailedExecutionReasons {
		m[r] = struct{}{}
	}
	m[ReasonMissed] = struct{}{}
	return m
}()

func isKnownReason(r EndReason) bool {
	return slices.Contains(AllEndReasons, r)
}

// ParseFailures parses `failures` TOML tokens into a reason set and inclusive
// exit-code ranges. A token is a single exit code ("42"), an inclusive exit
// range ("1-23"), or an EndReason name ("failed"). Pure and dependency-free so
// config, the importers, and tests share one grammar. Exit tokens are 1-255
// (exit 0 is always success); `succeeded` is rejected as a reason.
func ParseFailures(tokens []string) (reasons map[EndReason]struct{}, ranges [][2]int, err error) {
	reasons = make(map[EndReason]struct{}, len(tokens))
	for _, raw := range tokens {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			continue
		}
		lo, hi, isExit, exitErr := parseExitToken(tok)
		if exitErr != nil {
			return nil, nil, exitErr
		}
		if isExit {
			ranges = append(ranges, [2]int{lo, hi})
			continue
		}
		reason := EndReason(tok)
		if reason == ReasonSuccess {
			return nil, nil, fmt.Errorf("failures: %q cannot be treated as a failure", tok)
		}
		if !isKnownReason(reason) {
			return nil, nil, fmt.Errorf("failures: %q is not a known end reason or exit-code token", tok)
		}
		reasons[reason] = struct{}{}
	}
	return reasons, ranges, nil
}

// parseExitToken reports whether tok is an exit-code token and, if so, its
// inclusive [lo, hi] range. A token that does not begin with a digit is not an
// exit token (isExit=false, no error) so it falls through to reason parsing; a
// token that begins with a digit but is malformed or out of the 1-255 range is
// an error.
func parseExitToken(tok string) (lo, hi int, isExit bool, err error) {
	if tok[0] < '0' || tok[0] > '9' {
		return 0, 0, false, nil
	}
	if before, after, found := strings.Cut(tok, "-"); found {
		lo, err = strconv.Atoi(before)
		if err == nil {
			hi, err = strconv.Atoi(after)
		}
		if err != nil {
			return 0, 0, false, fmt.Errorf("failures: %q is not a valid exit-code range", tok)
		}
	} else {
		lo, err = strconv.Atoi(tok)
		if err != nil {
			return 0, 0, false, fmt.Errorf("failures: %q is not a valid exit code", tok)
		}
		hi = lo
	}
	if lo < 1 || hi > 255 || lo > hi {
		return 0, 0, false, fmt.Errorf("failures: exit token %q must be within 1-255, low-to-high (exit 0 is always success)", tok)
	}
	return lo, hi, true, nil
}
