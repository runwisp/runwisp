// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"fmt"
	"maps"
	"regexp"
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
// scheduled run. Reason tokens only — no exit ranges or output patterns (the
// `failed` reason already covers every non-zero exit by default).
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
	return spec.Resolve(FailureMatcher{}).Reasons
}()

func isKnownReason(r EndReason) bool {
	return slices.Contains(AllEndReasons, r)
}

// outputTokenPrefix marks a `failures` token as an output pattern:
// "output:<RE2 regex>" matches any captured stdout/stderr line.
const outputTokenPrefix = "output:"

// FailureMatcher is a resolved `failures` list — the concrete classification a
// task (or [defaults]) applies to terminal runs. See Task.IsFailureReason.
type FailureMatcher struct {
	// Reasons holds the end reasons that count as a failure. nil means "not
	// configured" (a Task built outside the config loader); IsFailureReason then
	// falls back to the built-in default set.
	Reasons map[EndReason]struct{}
	// ExitRanges are inclusive exit-code ranges that fail a `failed` run.
	ExitRanges [][2]int
	// OutputPatterns are RE2 sources from "output:" tokens, already validated by
	// ParseFailures. Kept as strings, not compiled, so config reload's
	// reflect.DeepEqual task diff stays reliable.
	OutputPatterns []string
}

// OutputRegexps compiles OutputPatterns. MustCompile is safe: every pattern
// entered through ParseFailures, which rejects anything that doesn't compile.
func (m FailureMatcher) OutputRegexps() []*regexp.Regexp {
	res := make([]*regexp.Regexp, len(m.OutputPatterns))
	for i, p := range m.OutputPatterns {
		res[i] = regexp.MustCompile(p)
	}
	return res
}

// DefaultFailures returns a fresh copy of the built-in failure classification —
// the resolved form of DefaultFailureTokens. It is both the matcher used when
// `failures` is omitted and the base a `[defaults]`-level delta adjusts.
func DefaultFailures() FailureMatcher {
	reasons := make(map[EndReason]struct{}, len(defaultFailureReasons))
	maps.Copy(reasons, defaultFailureReasons)
	return FailureMatcher{Reasons: reasons}
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
	// AddReasons/AddRanges/AddPatterns hold the whole set for an absolute spec,
	// or just the "+" tokens for a delta.
	AddReasons  map[EndReason]struct{}
	AddRanges   [][2]int
	AddPatterns []string
	// DropReasons/DropRanges/DropPatterns hold the "-" tokens; empty for an
	// absolute spec.
	DropReasons  map[EndReason]struct{}
	DropRanges   [][2]int
	DropPatterns []string
}

// ParseFailures parses `failures` TOML tokens into a FailureSpec. A token is a
// single exit code ("42"), an inclusive exit range ("1-23"), an EndReason
// name ("failed"), or an output pattern ("output:(?i)error"), optionally prefixed with "+" (add to the inherited set) or
// "-" (drop from it). A list either uses bare tokens (replace the whole set) or
// +/- deltas (adjust it) — mixing the two is rejected. Pure and dependency-free
// so config, the importers, and tests share one grammar. Exit tokens are 1-255
// (exit 0 can only fail through an output pattern); `succeeded` is rejected as a reason.
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

// addToken classifies one prefix-stripped token (output pattern, exit
// code/range, or reason) and records it in the add or drop set. raw is the
// original token, used only for error messages. `succeeded`, unknown reasons,
// and empty or non-compiling output patterns are rejected.
func (s *FailureSpec) addToken(tok, raw string, drop bool) error {
	if tok == "" {
		return fmt.Errorf("failures: %q is not a valid token", raw)
	}
	if pattern, ok := strings.CutPrefix(tok, outputTokenPrefix); ok {
		return s.addPattern(pattern, raw, drop)
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

// addPattern validates an "output:" token's RE2 pattern and records it in the
// add or drop set. An empty pattern is rejected: it would match every line.
func (s *FailureSpec) addPattern(pattern, raw string, drop bool) error {
	if pattern == "" {
		return fmt.Errorf("failures: %q needs a pattern after %q", raw, outputTokenPrefix)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("failures: %q is not a valid output pattern: %w", raw, err)
	}
	if drop {
		s.DropPatterns = append(s.DropPatterns, pattern)
	} else {
		s.AddPatterns = append(s.AddPatterns, pattern)
	}
	return nil
}

// Resolve turns the spec into a concrete failure matcher. An absolute spec
// returns its own set (copied, so callers may mutate); a delta returns base
// unioned with the "+" tokens and minus the "-" tokens — the drop applies to
// both the base ranges/patterns and this same delta's own added ones, so
// "+30-35", "-30-35" in one list correctly cancels out, matching reason
// tokens. An exit-range or output-pattern drop removes only an entry exactly
// equal to it — no interval splitting, no regex reasoning.
func (s *FailureSpec) Resolve(base FailureMatcher) FailureMatcher {
	if !s.Delta {
		reasons := make(map[EndReason]struct{}, len(s.AddReasons))
		maps.Copy(reasons, s.AddReasons)
		return FailureMatcher{
			Reasons:        reasons,
			ExitRanges:     slices.Clone(s.AddRanges),
			OutputPatterns: slices.Clone(s.AddPatterns),
		}
	}
	reasons := make(map[EndReason]struct{}, len(base.Reasons)+len(s.AddReasons))
	maps.Copy(reasons, base.Reasons)
	maps.Copy(reasons, s.AddReasons)
	for r := range s.DropReasons {
		delete(reasons, r)
	}
	return FailureMatcher{
		Reasons:        reasons,
		ExitRanges:     applyDelta(base.ExitRanges, s.AddRanges, s.DropRanges),
		OutputPatterns: applyDelta(base.OutputPatterns, s.AddPatterns, s.DropPatterns),
	}
}

// applyDelta returns base followed by add, minus every entry equal to one in
// drop. nil when nothing survives, so an unset list stays nil.
func applyDelta[T comparable](base, add, drop []T) []T {
	var out []T
	for _, v := range slices.Concat(base, add) {
		if !slices.Contains(drop, v) {
			out = append(out, v)
		}
	}
	return out
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
		return 0, 0, false, fmt.Errorf("failures: exit token %q must be within 1-255, low-to-high (exit 0 is not an exit-code failure)", tok)
	}
	return lo, hi, true, nil
}
