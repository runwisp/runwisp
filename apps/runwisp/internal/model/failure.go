// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// FailedExecutionReasons are the end reasons that represent a run which actually
// executed (or attempted to start) and failed — the only reasons where
// automatically re-running the command is meaningful. It is the fixed ceiling
// for auto retry/restart eligibility (runtime/retry.IsFailedExecution): a run
// only retries/restarts when its reason is also in this set AND the task's
// `failures` policy (Task.IsFailureReason) classifies it as a failure. It also
// seeds the default failure classification (DefaultFailureTokens).
//
// It is deliberately narrower than Run.IsRetryable (the operator-facing "may I
// re-run this?" affordance, which also covers stopped/timeout/etc.): promoting
// `stopped` into a task's `failures` list must never make the daemon
// automatically re-run a run the operator just stopped, since `stopped` is
// never in this set.
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

// defaultFailureReasons is DefaultFailureTokens parsed once — the single source
// of the built-in default set. Used as the fallback for a Task built outside the
// config loader (tests, ad-hoc dispatch) whose FailureReasons map is still nil,
// and as the base DefaultFailures hands out.
var defaultFailureReasons = func() map[EndReason]struct{} {
	spec, err := ParseFailures(DefaultFailureTokens)
	if err != nil {
		panic(fmt.Sprintf("model: DefaultFailureTokens is invalid: %v", err))
	}
	reasons, _ := spec.Resolve(nil, nil)
	return reasons
}()

func isKnownReason(r EndReason) bool {
	return slices.Contains(AllEndReasons, r)
}

// DefaultFailures returns a fresh copy of the built-in failure classification —
// the resolved form of DefaultFailureTokens. It is both the matcher used when
// `failures` is omitted and the base a `[defaults]`-level delta adjusts.
func DefaultFailures() (reasons map[EndReason]struct{}, ranges [][2]int) {
	reasons = make(map[EndReason]struct{}, len(defaultFailureReasons))
	maps.Copy(reasons, defaultFailureReasons)
	return reasons, nil
}

// FailureSpec is a parsed `failures` list. It is either an absolute set (bare
// tokens that replace the inherited base) or a delta (every token prefixed with
// + or -, mutating the base). Resolve turns it into a concrete matcher; the
// two-step parse-then-resolve exists because a delta can only be applied once
// the base it adjusts is known (the built-in default at `[defaults]`, the
// resolved `[defaults]` matcher at a task).
type FailureSpec struct {
	// Delta reports whether the tokens were +/- prefixed. Absolute specs ignore
	// the base; delta specs add/drop against it.
	Delta bool
	// AddReasons/AddRanges hold the whole set for an absolute spec, or just the
	// "+" tokens for a delta.
	AddReasons map[EndReason]struct{}
	AddRanges  [][2]int
	// DropReasons/DropRanges hold the "-" tokens; empty for an absolute spec.
	DropReasons map[EndReason]struct{}
	DropRanges  [][2]int
}

// ParseFailures parses `failures` TOML tokens into a FailureSpec. A token is a
// single exit code ("42"), an inclusive exit range ("1-23"), or an EndReason
// name ("failed"), optionally prefixed with "+" (add to the inherited set) or
// "-" (drop from it). A list either uses bare tokens (replace the whole set) or
// +/- deltas (adjust it) — mixing the two is rejected. Pure and dependency-free
// so config, the importers, and tests share one grammar. Exit tokens are 1-255
// (exit 0 is always success); `succeeded` is rejected as a reason.
func ParseFailures(tokens []string) (*FailureSpec, error) {
	spec := &FailureSpec{
		AddReasons:  map[EndReason]struct{}{},
		DropReasons: map[EndReason]struct{}{},
	}
	sawDelta, sawBare := false, false
	for _, raw := range tokens {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			continue
		}
		var drop, delta bool
		tok, drop, delta = stripFailurePrefix(tok)
		sawDelta = sawDelta || delta
		sawBare = sawBare || !delta
		if err := spec.addToken(tok, raw, drop); err != nil {
			return nil, err
		}
	}
	if sawDelta && sawBare {
		return nil, fmt.Errorf("failures: cannot mix bare tokens with +/- deltas — either replace the whole list or adjust it with + and -")
	}
	spec.Delta = sawDelta
	return spec, nil
}

// stripFailurePrefix peels an optional leading +/- off a token, reporting
// whether it was a drop ("-") and whether any prefix was present (delta).
func stripFailurePrefix(tok string) (rest string, drop, delta bool) {
	switch tok[0] {
	case '+':
		return strings.TrimSpace(tok[1:]), false, true
	case '-':
		return strings.TrimSpace(tok[1:]), true, true
	default:
		return tok, false, false
	}
}

// addToken classifies one prefix-stripped token (exit code/range or reason) and
// records it in the add or drop set. raw is the original token, used only for
// error messages. `succeeded` and unknown reasons are rejected.
func (s *FailureSpec) addToken(tok, raw string, drop bool) error {
	if tok == "" {
		return fmt.Errorf("failures: %q is not a valid token", raw)
	}
	lo, hi, isExit, err := parseExitToken(tok)
	if err != nil {
		return err
	}
	if isExit {
		if drop {
			s.DropRanges = append(s.DropRanges, [2]int{lo, hi})
		} else {
			s.AddRanges = append(s.AddRanges, [2]int{lo, hi})
		}
		return nil
	}
	reason := EndReason(tok)
	if reason == ReasonSuccess {
		return fmt.Errorf("failures: %q cannot be treated as a failure", tok)
	}
	if !isKnownReason(reason) {
		return fmt.Errorf("failures: %q is not a known end reason or exit-code token", tok)
	}
	if drop {
		s.DropReasons[reason] = struct{}{}
	} else {
		s.AddReasons[reason] = struct{}{}
	}
	return nil
}

// Resolve turns the spec into a concrete failure matcher. An absolute spec
// returns its own set (copied, so callers may mutate); a delta returns base
// unioned with the "+" tokens and minus the "-" tokens — the drop applies to
// both the base ranges and this same delta's own added ranges, so "+30-35",
// "-30-35" in one list correctly cancels out, matching reason tokens. An
// exit-range drop removes only a range exactly equal to it — no interval
// splitting.
func (s *FailureSpec) Resolve(baseReasons map[EndReason]struct{}, baseRanges [][2]int) (map[EndReason]struct{}, [][2]int) {
	if !s.Delta {
		reasons := make(map[EndReason]struct{}, len(s.AddReasons))
		maps.Copy(reasons, s.AddReasons)
		return reasons, slices.Clone(s.AddRanges)
	}
	reasons := make(map[EndReason]struct{}, len(baseReasons)+len(s.AddReasons))
	maps.Copy(reasons, baseReasons)
	maps.Copy(reasons, s.AddReasons)
	for r := range s.DropReasons {
		delete(reasons, r)
	}
	var ranges [][2]int
	for _, r := range baseRanges {
		if !slices.Contains(s.DropRanges, r) {
			ranges = append(ranges, r)
		}
	}
	for _, r := range s.AddRanges {
		if !slices.Contains(s.DropRanges, r) {
			ranges = append(ranges, r)
		}
	}
	return reasons, ranges
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
