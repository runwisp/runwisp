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

// TestValidateTaskName_RejectsBadInputs locks in the shared validator that
// the TOML loader, REST inputs, and the cloud-dispatch resolver all rely
// on. A drift here is a latent bug: a name accepted by TOML but rejected
// by the API (or vice versa) makes the same task unreachable from
// surfaces that should round-trip cleanly.
func TestValidateTaskName_RejectsBadInputs(t *testing.T) {
	bad := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"contains slash", "foo/bar"},
		{"contains space", "foo bar"},
		{"contains colon", "foo:bar"},
		{"contains hash", "foo#bar"},
		{"too long", strings.Repeat("a", TaskNameMaxLength+1)},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			assert.Error(t, ValidateTaskName(tc.in))
		})
	}
}

func TestValidateTaskName_AcceptsGoodInputs(t *testing.T) {
	for _, name := range []string{
		"task1",
		"my-task",
		"my_task",
		"task.with.dots",
		"a",
		strings.Repeat("a", TaskNameMaxLength),
	} {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, ValidateTaskName(name))
		})
	}
}

// TestTaskNamePatternString_MatchesPattern keeps the compiled regex and
// the documented huma pattern literal in lockstep — if a future change
// edits one without the other, this catches it before it ships.
func TestTaskNamePatternString_MatchesPattern(t *testing.T) {
	assert.Equal(t, TaskNamePatternString, TaskNamePattern.String())
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
