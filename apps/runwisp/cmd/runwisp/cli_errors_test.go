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
