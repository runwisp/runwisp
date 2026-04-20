// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModelConstants(t *testing.T) {
	assert.Equal(t, RunPhase("pending"), PhasePending)
	assert.Equal(t, TriggeredBy("cron"), TriggeredByCron)
	assert.Equal(t, ConcurrencyPolicy("queue"), PolicyQueue)
}
