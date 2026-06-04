// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsUserFacing_Direct(t *testing.T) {
	e := &userFacingError{title: "direct error"}
	got, ok := isUserFacing(e)
	require.True(t, ok)
	assert.Equal(t, "direct error", got.title)
}

func TestIsUserFacing_Wrapped(t *testing.T) {
	inner := &userFacingError{title: "inner"}
	wrapped := fmt.Errorf("outer: %w", inner)
	got, ok := isUserFacing(wrapped)
	require.True(t, ok)
	assert.Equal(t, "inner", got.title)
}

func TestIsUserFacing_NotUserFacing(t *testing.T) {
	_, ok := isUserFacing(errors.New("plain error"))
	assert.False(t, ok)
}

func TestIsUserFacing_Nil(t *testing.T) {
	_, ok := isUserFacing(nil)
	assert.False(t, ok)
}

func TestUnknownTaskError(t *testing.T) {
	t.Run("suggests closest match and lists tasks", func(t *testing.T) {
		err := unknownTaskError("backap", []string{"cleanup", "backup"})
		msg := err.Error()
		assert.Contains(t, msg, `task "backap" not found`)
		assert.Contains(t, msg, `Did you mean "backup"?`)
		assert.Contains(t, msg, "Available tasks:")
		assert.Contains(t, msg, "- backup")
		assert.Contains(t, msg, "- cleanup")
	})

	t.Run("no suggestion for gibberish", func(t *testing.T) {
		err := unknownTaskError("zzqxwy", []string{"backup"})
		assert.NotContains(t, err.Error(), "Did you mean")
		assert.Contains(t, err.Error(), "- backup")
	})

	t.Run("empty task set points at runwisp.toml", func(t *testing.T) {
		err := unknownTaskError("anything", nil)
		assert.Contains(t, err.Error(), "No tasks are defined")
		assert.Contains(t, err.Error(), "runwisp.toml")
	})
}
