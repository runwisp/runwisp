// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelConstants(t *testing.T) {
	assert.Equal(t, RunPhase("pending"), PhasePending)
	assert.Equal(t, TriggeredBy("cron"), TriggeredByCron)
	assert.Equal(t, ConcurrencyPolicy("queue"), PolicyQueue)
}

// TestDaemonInfo_JSONShapeIsLocked guards the /api/info wire shape against
// silent additions of password / credential / ephemeral fields. The struct is
// the surface a future contributor would touch when "exposing the password to
// the Web UI for convenience" — locking the marshaled key set turns that into
// an explicit test failure rather than a quiet release.
func TestDaemonInfo_JSONShapeIsLocked(t *testing.T) {
	raw, err := json.Marshal(DaemonInfo{})
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &decoded))

	want := []string{
		"capabilities",
		"cloud_enabled",
		"fingerprint",
		"port",
		"resolved_timezone",
		"tasks",
		"timezone_source",
		"version",
	}
	got := make([]string, 0, len(decoded))
	for k := range decoded {
		got = append(got, k)
	}
	sort.Strings(got)
	assert.Equal(t, want, got,
		"DaemonInfo's marshaled top-level keys must match the documented /api/info shape exactly")

	lower := strings.ToLower(string(raw))
	for _, banned := range []string{"password", "credential", "ephemeral"} {
		assert.NotContains(t, lower, banned,
			"DaemonInfo must not gain a %q-related field — that data belongs on the local-only credentials endpoint", banned)
	}
}
