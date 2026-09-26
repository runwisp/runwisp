// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hasReason / hasRange are tiny readers so the assertions below stay legible.
func hasReason(reasons map[EndReason]struct{}, r EndReason) bool {
	_, ok := reasons[r]
	return ok
}

func TestParseFailures_Absolute(t *testing.T) {
	spec, err := ParseFailures([]string{"timeout", "23", "30-35"})
	require.NoError(t, err)
	assert.False(t, spec.Delta, "a bare list is an absolute spec")

	m := spec.Resolve(DefaultFailures())
	assert.True(t, hasReason(m.Reasons, ReasonTimeout))
	assert.False(t, hasReason(m.Reasons, ReasonFailed),
		"an absolute spec replaces the base — it does not inherit failed")
	assert.Equal(t, [][2]int{{23, 23}, {30, 35}}, m.ExitRanges)
}

func TestParseFailures_DeltaAddDrop(t *testing.T) {
	spec, err := ParseFailures([]string{"+stopped", "-missed"})
	require.NoError(t, err)
	assert.True(t, spec.Delta)

	reasons := spec.Resolve(DefaultFailures()).Reasons
	assert.True(t, hasReason(reasons, ReasonStopped), "+stopped adds")
	assert.False(t, hasReason(reasons, ReasonMissed), "-missed drops")
	assert.True(t, hasReason(reasons, ReasonFailed), "untouched base reasons stay")
}

func TestParseFailures_DeltaExitRanges(t *testing.T) {
	spec, err := ParseFailures([]string{"+42", "-1-23"})
	require.NoError(t, err)
	m := spec.Resolve(FailureMatcher{ExitRanges: [][2]int{{1, 23}, {90, 99}}})
	assert.Empty(t, m.Reasons)
	// -1-23 drops the exactly-matching base range; +42 is appended.
	assert.Equal(t, [][2]int{{90, 99}, {42, 42}}, m.ExitRanges)
}

func TestParseFailures_DeltaExitRangeSelfCancels(t *testing.T) {
	spec, err := ParseFailures([]string{"+30-35", "-30-35"})
	require.NoError(t, err)
	assert.Empty(t, spec.Resolve(FailureMatcher{}).ExitRanges, "a range added and dropped in the same list must not survive, matching +/-reason self-cancel")
}

func TestParseFailures_ResolveCopiesBase(t *testing.T) {
	spec, err := ParseFailures([]string{"+stopped"})
	require.NoError(t, err)
	base := DefaultFailures()
	reasons := spec.Resolve(base).Reasons
	reasons[ReasonSkipped] = struct{}{}
	_, mutated := base.Reasons[ReasonSkipped]
	assert.False(t, mutated, "Resolve must not mutate the caller's base set")
}

func TestParseFailures_OutputPatterns(t *testing.T) {
	spec, err := ParseFailures([]string{"output:(?i)fatal", "timeout"})
	require.NoError(t, err)
	m := spec.Resolve(DefaultFailures())
	assert.Equal(t, []string{"(?i)fatal"}, m.OutputPatterns)
	assert.False(t, hasReason(m.Reasons, ReasonFailed), "an absolute spec replaces the base")

	spec, err = ParseFailures([]string{"+output:boom", "-output:old", "+output:gone", "-output:gone"})
	require.NoError(t, err)
	m = spec.Resolve(FailureMatcher{OutputPatterns: []string{"old", "kept"}})
	// -old drops the exactly-matching base pattern; +gone/-gone cancel out.
	assert.Equal(t, []string{"kept", "boom"}, m.OutputPatterns)
	assert.Len(t, m.OutputRegexps(), 2)
}

func TestIsFailureReason_OutputMatched(t *testing.T) {
	spec, err := ParseFailures([]string{"timeout", "output:ERROR"})
	require.NoError(t, err)
	task := &Task{Failures: spec.Resolve(DefaultFailures())}
	assert.True(t, task.IsFailureReason(ReasonFailed, 0, true),
		"an output match fails the run even without failed in the list")
	assert.True(t, task.IsFailureReason(ReasonFailed, 1, true))
	assert.False(t, task.IsFailureReason(ReasonFailed, 1, false),
		"other non-zero exits don't fail under an absolute list without failed")
	assert.False(t, task.IsFailureReason(ReasonStopped, 0, true),
		"output matches only classify runs that exited")
	assert.False(t, task.IsFailureReason(ReasonSuccess, 0, false))
}

func TestParseFailures_Rejects(t *testing.T) {
	cases := map[string][]string{
		"mixing bare and delta": {"failed", "-missed"},
		"succeeded bare":        {"succeeded"},
		"succeeded delta":       {"+succeeded"},
		"unknown reason":        {"nope"},
		"exit out of range":     {"300"},
		"empty output pattern":  {"output:"},
		"invalid output regex":  {"output:("},
		"mixing output delta":   {"output:x", "+missed"},
		"exit zero":             {"0"},
		"lone minus":            {"-"},
	}
	for name, tokens := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseFailures(tokens)
			require.Error(t, err)
		})
	}
}
