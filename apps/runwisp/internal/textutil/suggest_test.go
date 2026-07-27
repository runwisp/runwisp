// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package textutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClosest(t *testing.T) {
	candidates := []string{"cron", "timeout", "graceful_stop", "run"}
	assert.Equal(t, "cron", Closest("corn", candidates))
	assert.Equal(t, "timeout", Closest("timeuot", candidates))
	assert.Equal(t, "timeout", Closest("TIMEUOT", candidates))
	assert.Equal(t, "", Closest("zzqxwy", candidates))
	// Threshold scales with length: a 2-edit typo on a short key still hits.
	assert.Equal(t, "run", Closest("rnu", candidates))
	assert.Equal(t, "", Closest("anything", nil))
}

func TestLevenshtein(t *testing.T) {
	assert.Equal(t, 0, Levenshtein("abc", "abc"))
	assert.Equal(t, 1, Levenshtein("abc", "abd"))
	assert.Equal(t, 3, Levenshtein("", "abc"))
	assert.Equal(t, 2, Levenshtein("timeuot", "timeout"))
}
