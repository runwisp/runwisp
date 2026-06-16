// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecodeParams_NullAndEmptyYieldNil(t *testing.T) {
	assert.Nil(t, decodeParams(nil, "run-1"))
	empty := ""
	assert.Nil(t, decodeParams(&empty, "run-1"))
}

func TestDecodeParams_ValidRoundTrips(t *testing.T) {
	raw := `{"--region":"eu","source":"/data"}`
	got := decodeParams(&raw, "run-1")
	assert.Equal(t, map[string]string{"--region": "eu", "source": "/data"}, got)
}

func TestDecodeParams_CorruptDegradesToNil(t *testing.T) {
	// A malformed params_json must never fail the row — the run still exists,
	// only its params are unreadable — so one corrupt row can't wedge a batch
	// like GetPendingRuns on boot.
	raw := `{not json`
	assert.Nil(t, decodeParams(&raw, "run-1"))
}
