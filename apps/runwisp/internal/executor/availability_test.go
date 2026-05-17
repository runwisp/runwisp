// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestForType_Unknown(t *testing.T) {
	a := &Availability{}
	s := a.ForType("wasm")
	assert.False(t, s.Available)
	assert.Contains(t, s.Reason, "unknown")
}
