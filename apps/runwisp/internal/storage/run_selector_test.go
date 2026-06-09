// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestBuildRunFilterArgs_EmptyFilterDisablesEveryGate(t *testing.T) {
	args := buildRunFilterArgs(model.RunFilter{})
	assert.Nil(t, args.EndReasonFilter)
	assert.Nil(t, args.StatusPhaseFilter)
	assert.Nil(t, args.TaskNameFilter)
	assert.Nil(t, args.SearchFilter)
	assert.Equal(t, "", args.SearchPattern)
}

func TestBuildRunFilterArgs_EndReasonStatusGoesToReasonColumn(t *testing.T) {
	args := buildRunFilterArgs(model.RunFilter{Status: string(model.ReasonSuccess)})
	assert.Equal(t, "success", args.EndReasonFilter)
	assert.Nil(t, args.StatusPhaseFilter)
}

func TestBuildRunFilterArgs_PhaseStatusGoesToStatusColumn(t *testing.T) {
	args := buildRunFilterArgs(model.RunFilter{Status: string(model.PhaseRunning)})
	assert.Nil(t, args.EndReasonFilter)
	assert.Equal(t, "running", args.StatusPhaseFilter)
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
