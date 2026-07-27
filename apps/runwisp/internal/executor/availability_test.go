// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

func TestForType_Compose(t *testing.T) {
	a := &Availability{Compose: BackendStatus{Available: true}}
	s := a.ForType("compose")
	assert.True(t, s.Available)
	assert.Empty(t, s.Reason)
}

func TestForType_ComposeUnavailableReasonFlowsThrough(t *testing.T) {
	a := &Availability{Compose: BackendStatus{Available: false, Reason: "docker compose CLI unavailable"}}
	s := a.ForType("compose")
	assert.False(t, s.Available)
	assert.Equal(t, "docker compose CLI unavailable", s.Reason)
}
