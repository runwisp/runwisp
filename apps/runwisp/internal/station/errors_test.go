// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStationError_Error_NoWrappedErr(t *testing.T) {
	e := &StationError{Kind: StationErrorKindValidation, Message: "bad payload"}
	assert.Equal(t, "VALIDATION_ERROR: bad payload", e.Error())
}

func TestStationError_Error_WithWrappedErr(t *testing.T) {
	inner := errors.New("boom")
	e := &StationError{Kind: StationErrorKindTransient, Message: "downstream", Err: inner}
	assert.Equal(t, "TRANSIENT_ERROR: downstream: boom", e.Error())
}

func TestStationError_Unwrap(t *testing.T) {
	inner := errors.New("root")
	e := &StationError{Kind: StationErrorKindAuth, Message: "x", Err: inner}
	assert.Same(t, inner, e.Unwrap())

	// errors.Is should traverse the chain.
	assert.True(t, errors.Is(e, inner))
}

func TestStationError_Unwrap_Nil(t *testing.T) {
	e := &StationError{Kind: StationErrorKindAuth, Message: "x"}
	assert.Nil(t, e.Unwrap())
}
