// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// runFilterGateArgs returns 9 positional params; document the slot indices
// here so the SQL <-> Go contract is greppable from one place.
const (
	endReasonGateIdx   = 0
	endReasonValueIdx  = 1
	statusGateIdx      = 2
	statusValueIdx     = 3
	taskNameGateIdx    = 4
	taskNameValueIdx   = 5
	searchGateIdx      = 6
	searchPattern1Idx  = 7
	searchPattern2Idx  = 8
	filterGateArgCount = 9
)

func TestRunFilterGateArgs_EmptyFilterDisablesEveryGate(t *testing.T) {
	args := runFilterGateArgs(model.RunFilter{})
	assert.Len(t, args, filterGateArgCount)
	for i, v := range args {
		assert.Equal(t, "", v, "args[%d] should be empty", i)
	}
}

func TestRunFilterGateArgs_EndReasonStatusGoesToReasonColumn(t *testing.T) {
	args := runFilterGateArgs(model.RunFilter{Status: string(model.ReasonSuccess)})
	assert.Equal(t, "success", args[endReasonGateIdx])
	assert.Equal(t, "success", args[endReasonValueIdx])
	assert.Equal(t, "", args[statusGateIdx])
	assert.Equal(t, "", args[statusValueIdx])
}

func TestRunFilterGateArgs_PhaseStatusGoesToStatusColumn(t *testing.T) {
	args := runFilterGateArgs(model.RunFilter{Status: string(model.PhaseRunning)})
	assert.Equal(t, "", args[endReasonGateIdx])
	assert.Equal(t, "", args[endReasonValueIdx])
	assert.Equal(t, "running", args[statusGateIdx])
	assert.Equal(t, "running", args[statusValueIdx])
}

func TestRunFilterGateArgs_TaskNameFillsBothSlots(t *testing.T) {
	args := runFilterGateArgs(model.RunFilter{TaskName: "task1"})
	assert.Equal(t, "task1", args[taskNameGateIdx])
	assert.Equal(t, "task1", args[taskNameValueIdx])
}

func TestRunFilterGateArgs_SearchBuildsLikePattern(t *testing.T) {
	args := runFilterGateArgs(model.RunFilter{Search: "foo"})
	assert.Equal(t, "foo", args[searchGateIdx])
	assert.Equal(t, "%foo%", args[searchPattern1Idx])
	assert.Equal(t, "%foo%", args[searchPattern2Idx])
}

func TestRunFilterGateArgs_SearchStripsLikeWildcards(t *testing.T) {
	// `%` and `_` are LIKE metacharacters — strip them so a user typing
	// them doesn't get an accidental wildcard match.
	args := runFilterGateArgs(model.RunFilter{Search: "%a_b%"})
	assert.Equal(t, "%a_b%", args[searchGateIdx], "gate keeps the raw input")
	assert.Equal(t, "%ab%", args[searchPattern1Idx])
}

func TestRunFilterGateArgs_SearchTruncatedToMaxLength(t *testing.T) {
	long := strings.Repeat("a", MaxSearchQueryLength+50)
	args := runFilterGateArgs(model.RunFilter{Search: long})
	pattern, ok := args[searchPattern1Idx].(string)
	assert.True(t, ok)
	assert.Len(t, pattern, MaxSearchQueryLength+2, "pattern is truncated body wrapped in %%")
}

func TestIDsJSON_EmptyInputYieldsEmptyArray(t *testing.T) {
	// "[]" makes `id IN (json_each('[]'))` an empty IN, which matches
	// nothing — exactly the safety-net the old "(1=0)" sentinel provided.
	assert.Equal(t, "[]", idsJSON(nil))
	assert.Equal(t, "[]", idsJSON([]string{}))
}

func TestIDsJSON_PopulatedInputProducesJSONArray(t *testing.T) {
	assert.Equal(t, `["a"]`, idsJSON([]string{"a"}))
	assert.Equal(t, `["a","b","c"]`, idsJSON([]string{"a", "b", "c"}))
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
