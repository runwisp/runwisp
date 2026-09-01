// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestBuildRunFilterArgs_EmptyFilterDisablesEveryGate(t *testing.T) {
	args := buildRunFilterArgs(model.RunFilter{})
	assert.Nil(t, args.StatusSet)
	assert.Nil(t, args.TaskNameFilter)
	assert.Nil(t, args.SearchFilter)
	assert.Nil(t, args.CreatedAfter)
	assert.Nil(t, args.CreatedBefore)
	assert.Nil(t, args.TriggeredByFilter)
	assert.Nil(t, args.ExitCodeMin)
	assert.Nil(t, args.ExitCodeMax)
	assert.Nil(t, args.RetriesOnly)
	assert.Equal(t, "", args.SearchPattern)
}

func TestBuildRunFilterArgs_StatusSetRendering(t *testing.T) {
	// The status gate is a single scalar set: one value is a one-element set,
	// many values are pipe-delimited, blanks are dropped, and an all-blank
	// input leaves the gate open (nil). Phases and end reasons share the set —
	// the SQL gate matches a run's phase OR its end reason against it.
	cases := []struct {
		name string
		in   string
		want interface{}
	}{
		{"empty", "", nil},
		{"all-blank", " , , ", nil},
		{"single end reason", string(model.ReasonSuccess), "|succeeded|"},
		{"single phase", string(model.PhaseRunning), "|running|"},
		{"multi", "failed,crashed,timeout", "|failed|crashed|timeout|"},
		{"trims and skips blanks", " failed , , crashed ", "|failed|crashed|"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := buildRunFilterArgs(model.RunFilter{Status: tc.in})
			assert.Equal(t, tc.want, args.StatusSet)
		})
	}
}

func TestBuildRunFilterArgs_NewScalarGates(t *testing.T) {
	after := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	before := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	lo, hi := 100, 150
	args := buildRunFilterArgs(model.RunFilter{
		CreatedAfter:  &after,
		CreatedBefore: &before,
		TriggeredBy:   string(model.TriggeredByAPI),
		ExitCodeMin:   &lo,
		ExitCodeMax:   &hi,
		RetriesOnly:   true,
	})
	assert.Equal(t, after, args.CreatedAfter)
	assert.Equal(t, before, args.CreatedBefore)
	assert.Equal(t, string(model.TriggeredByAPI), args.TriggeredByFilter)
	assert.Equal(t, 100, args.ExitCodeMin)
	assert.Equal(t, 150, args.ExitCodeMax)
	assert.Equal(t, 1, args.RetriesOnly, "true closes the gate with a non-nil sentinel")
}

func TestBuildRunFilterArgs_ExitCodeBoundsAreIndependent(t *testing.T) {
	// Either bound may be set alone; the other side stays open (nil). A bound
	// of 0 is a real value, distinct from absent.
	min0 := 0
	lower := buildRunFilterArgs(model.RunFilter{ExitCodeMin: &min0})
	assert.Equal(t, 0, lower.ExitCodeMin)
	assert.Nil(t, lower.ExitCodeMax)

	max0 := 0
	upper := buildRunFilterArgs(model.RunFilter{ExitCodeMax: &max0})
	assert.Nil(t, upper.ExitCodeMin)
	assert.Equal(t, 0, upper.ExitCodeMax)
}

func TestBuildRunFilterArgs_TaskNameFillsTaskNameFilter(t *testing.T) {
	args := buildRunFilterArgs(model.RunFilter{TaskName: "task1"})
	assert.Equal(t, "task1", args.TaskNameFilter)
}

func TestBuildRunFilterArgs_SearchBuildsLikePattern(t *testing.T) {
	args := buildRunFilterArgs(model.RunFilter{Search: "foo"})
	assert.Equal(t, "foo", args.SearchFilter)
	assert.Equal(t, "%foo%", args.SearchPattern)
}

func TestBuildRunFilterArgs_SearchStripsLikeWildcards(t *testing.T) {
	// `%` and `_` are LIKE metacharacters — strip them so a user typing
	// them doesn't get an accidental wildcard match.
	args := buildRunFilterArgs(model.RunFilter{Search: "%a_b%"})
	assert.Equal(t, "%a_b%", args.SearchFilter, "gate keeps the raw input")
	assert.Equal(t, "%ab%", args.SearchPattern)
}

func TestBuildRunFilterArgs_SearchTruncatedToMaxLength(t *testing.T) {
	long := strings.Repeat("a", MaxSearchQueryLength+50)
	args := buildRunFilterArgs(model.RunFilter{Search: long})
	assert.Len(t, args.SearchPattern, MaxSearchQueryLength+2, "pattern is truncated body wrapped in %%")
}

func TestBuildRunFilterArgs_SearchTruncationRespectsUTF8Boundary(t *testing.T) {
	// A 3-byte rune repeated so the byte-100 cut point lands mid-rune: 33 full
	// runes take 99 bytes, so a raw s[:100] slice would keep only the first
	// byte of the 34th rune, producing an invalid UTF-8 tail.
	long := strings.Repeat("中", 40)
	args := buildRunFilterArgs(model.RunFilter{Search: long})

	assert.True(t, utf8.ValidString(args.SearchPattern), "search pattern must not split a multi-byte rune")
}

func TestExceptIDsForSlice_EmptyInputYieldsSentinelOnly(t *testing.T) {
	// Empty input must still produce a one-element slice so sqlc.slice
	// expands to `NOT IN ('')` — matching every real (non-empty) ULID.
	got := exceptIDsForSlice(nil)
	assert.Equal(t, []string{""}, got)
	got = exceptIDsForSlice([]string{})
	assert.Equal(t, []string{""}, got)
}

func TestExceptIDsForSlice_PopulatedInputPrependsSentinel(t *testing.T) {
	got := exceptIDsForSlice([]string{"a", "b"})
	assert.Equal(t, []string{"", "a", "b"}, got)
}

func TestRunSelector_Validate(t *testing.T) {
	// MatchAll with IDs is a misuse — exactly one mode must be active.
	err := model.RunSelector{MatchAll: true, IDs: []string{"a"}}.Validate()
	assert.Error(t, err)

	// Explicit mode with empty IDs is rejected.
	err = model.RunSelector{MatchAll: false}.Validate()
	assert.Error(t, err)

	// Valid explicit selector.
	err = model.RunSelector{IDs: []string{"a"}}.Validate()
	assert.NoError(t, err)

	// Valid MatchAll selector.
	err = model.RunSelector{MatchAll: true}.Validate()
	assert.NoError(t, err)
}
