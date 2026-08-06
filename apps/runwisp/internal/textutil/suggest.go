// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package textutil holds small string helpers shared across the daemon and
// CLI, kept dependency-free so any package can import it.
package textutil

import (
	"fmt"
	"strings"
)

// Pluralize returns singular when n == 1, otherwise plural. Use Count instead
// when the sentence also needs to print n itself.
func Pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// Count renders n alongside the correctly pluralized noun, e.g.
// Count(1, "task", "tasks") == "1 task".
func Count(n int, singular, plural string) string {
	return fmt.Sprintf("%d %s", n, Pluralize(n, singular, plural))
}

// Closest returns the candidate closest to target by levenshtein distance,
// or "" when nothing is within the typo threshold of max(2, len/3).
// Case-insensitive, so SCHEDLUE still suggests schedule.
func Closest(target string, candidates []string) string {
	lowered := strings.ToLower(target)
	threshold := max(2, len(lowered)/3)
	best, bestDist := "", threshold+1
	for _, candidate := range candidates {
		d := levenshtein(lowered, strings.ToLower(candidate))
		if d < bestDist {
			best, bestDist = candidate, d
		}
	}
	return best
}

// levenshtein computes edit distance with the classic two-row algorithm.
// Inputs are config keys and task names (short ASCII), so byte-wise
// comparison is fine.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
