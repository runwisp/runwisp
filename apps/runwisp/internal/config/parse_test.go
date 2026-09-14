// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	t.Run("empty is zero", func(t *testing.T) {
		d, err := parseDuration("")
		require.NoError(t, err)
		assert.Equal(t, time.Duration(0), d)
	})

	t.Run("valid forms", func(t *testing.T) {
		for raw, want := range map[string]time.Duration{
			"30s":   30 * time.Second,
			"5m":    5 * time.Minute,
			"2h30m": 2*time.Hour + 30*time.Minute,
		} {
			d, err := parseDuration(raw)
			require.NoError(t, err, raw)
			assert.Equal(t, want, d, raw)
		}
	})

	// Regression: the schema's shared $defs/duration advertises d/w suffixes for
	// every duration field (timeout, retry_delay, jitter, ...), but until this
	// fix only keep_for actually accepted them — timeout = "2d" validated
	// against the published schema yet failed `runwisp validate`.
	t.Run("day and week suffixes work outside keep_for too", func(t *testing.T) {
		for raw, want := range map[string]time.Duration{
			"2d": 2 * 24 * time.Hour,
			"1w": 7 * 24 * time.Hour,
		} {
			d, err := parseDuration(raw)
			require.NoError(t, err, raw)
			assert.Equal(t, want, d, raw)
		}
	})

	t.Run("invalid input gets a hint, not Go's raw error", func(t *testing.T) {
		for _, raw := range []string{"5 minutes", "bad", "10"} {
			_, err := parseDuration(raw)
			require.Error(t, err, raw)
			assert.Contains(t, err.Error(), "is not a valid duration", raw)
			assert.Contains(t, err.Error(), `"2h30m"`, raw)
			assert.NotContains(t, err.Error(), "time: ", raw)
		}
	})
}

func TestParseKeepForHint(t *testing.T) {
	_, err := parseKeepFor("1 month")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a valid duration")
	assert.Contains(t, err.Error(), `"2d"`)
	assert.Contains(t, err.Error(), `"1w"`)
	assert.NotContains(t, err.Error(), "time: ")
}

func TestParseKeepForRejectsNonPositive(t *testing.T) {
	for _, raw := range []string{"0s", "-5m"} {
		_, err := parseKeepFor(raw)
		require.Error(t, err, raw)
		assert.Contains(t, err.Error(), "non-positive duration", raw)
	}
}
