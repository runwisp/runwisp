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

	reasons, ranges := spec.Resolve(defaultFailureReasons, nil)
	assert.True(t, hasReason(reasons, ReasonTimeout))
	assert.False(t, hasReason(reasons, ReasonFailed),
		"an absolute spec replaces the base — it does not inherit failed")
	assert.Equal(t, [][2]int{{23, 23}, {30, 35}}, ranges)
}

func TestParseFailures_DeltaAddDrop(t *testing.T) {
	spec, err := ParseFailures([]string{"+stopped", "-missed"})
	require.NoError(t, err)
	assert.True(t, spec.Delta)

	base, _ := DefaultFailures()
	reasons, _ := spec.Resolve(base, nil)
	assert.True(t, hasReason(reasons, ReasonStopped), "+stopped adds")
	assert.False(t, hasReason(reasons, ReasonMissed), "-missed drops")
	assert.True(t, hasReason(reasons, ReasonFailed), "untouched base reasons stay")
}

func TestParseFailures_DeltaExitRanges(t *testing.T) {
	spec, err := ParseFailures([]string{"+42", "-1-23"})
	require.NoError(t, err)
	reasons, ranges := spec.Resolve(nil, [][2]int{{1, 23}, {90, 99}})
	assert.Empty(t, reasons)
	// -1-23 drops the exactly-matching base range; +42 is appended.
	assert.Equal(t, [][2]int{{90, 99}, {42, 42}}, ranges)
}

func TestParseFailures_ResolveCopiesBase(t *testing.T) {
	spec, err := ParseFailures([]string{"+stopped"})
	require.NoError(t, err)
	base, _ := DefaultFailures()
	reasons, _ := spec.Resolve(base, nil)
	reasons[ReasonSkipped] = struct{}{}
	_, mutated := base[ReasonSkipped]
	assert.False(t, mutated, "Resolve must not mutate the caller's base set")
}

func TestParseFailures_Rejects(t *testing.T) {
	cases := map[string][]string{
		"mixing bare and delta": {"failed", "-missed"},
		"succeeded bare":        {"succeeded"},
		"succeeded delta":       {"+succeeded"},
		"unknown reason":        {"nope"},
		"exit out of range":     {"300"},
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
