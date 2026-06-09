// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserFacingError_ErrorTitleOnly(t *testing.T) {
	e := &userFacingError{title: "boom"}
	assert.Equal(t, "boom", e.Error())
}

func TestUserFacingError_ErrorWithDetails(t *testing.T) {
	e := &userFacingError{title: "boom", details: "more context"}
	out := e.Error()
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "more context")
}

func TestRenderUserFacingError_TitleOnly(t *testing.T) {
	var buf bytes.Buffer
	renderUserFacingError(&buf, &userFacingError{title: "boom"})
	out := buf.String()
	assert.Contains(t, out, "Error")
	assert.Contains(t, out, "boom")
}

func TestRenderUserFacingError_WithDetailsAndBullets(t *testing.T) {
	var buf bytes.Buffer
	renderUserFacingError(&buf, &userFacingError{
		title:   "boom",
		details: "context line\n  - fix one\n  - fix two\n",
	})
	out := buf.String()
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "context line")
	assert.Contains(t, out, "fix one")
	assert.Contains(t, out, "fix two")
}

func TestPasswordMismatchError_PortInTitle(t *testing.T) {
	err := passwordMismatchError(9477)
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "9477")
	assert.Contains(t, ufe.details, "RUNWISP_PASSWORD")
}

func TestTUIPasswordMismatchError_Explicit(t *testing.T) {
	err := tuiPasswordMismatchError(9477, true)
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "does not match")
	assert.Empty(t, ufe.details)
}

func TestTUIPasswordMismatchError_Implicit(t *testing.T) {
	err := tuiPasswordMismatchError(9477, false)
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "password mismatch")
	assert.Contains(t, ufe.details, "--password")
}

func TestAuthRateLimitedError_HasHints(t *testing.T) {
	err := authRateLimitedError(9477)
	var ufe *userFacingError
	require.True(t, errors.As(err, &ufe))
	assert.Contains(t, ufe.title, "9477")
	assert.Contains(t, ufe.details, "Wait")
}

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
