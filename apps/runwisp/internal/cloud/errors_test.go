// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCloudError_Error_NoWrappedErr(t *testing.T) {
	e := &CloudError{Kind: CloudErrorKindValidation, Message: "bad payload"}
	assert.Equal(t, "VALIDATION_ERROR: bad payload", e.Error())
}

func TestCloudError_Error_WithWrappedErr(t *testing.T) {
	inner := errors.New("boom")
	e := &CloudError{Kind: CloudErrorKindTransient, Message: "downstream", Err: inner}
	assert.Equal(t, "TRANSIENT_ERROR: downstream: boom", e.Error())
}

func TestCloudError_Unwrap(t *testing.T) {
	inner := errors.New("root")
	e := &CloudError{Kind: CloudErrorKindAuth, Message: "x", Err: inner}
	assert.Same(t, inner, e.Unwrap())

	// errors.Is should traverse the chain.
	assert.True(t, errors.Is(e, inner))
}

func TestCloudError_Unwrap_Nil(t *testing.T) {
	e := &CloudError{Kind: CloudErrorKindAuth, Message: "x"}
	assert.Nil(t, e.Unwrap())
}
