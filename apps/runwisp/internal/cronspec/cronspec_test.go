// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cronspec

import (
	"testing"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		timezone string
		wantErr  bool
	}{
		{name: "five fields", spec: "0 3 * * *"},
		{name: "step", spec: "*/5 * * * *"},
		{name: "descriptor hourly", spec: "@hourly"},
		{name: "descriptor every", spec: "@every 1h30m"},
		{name: "with timezone", spec: "0 3 * * *", timezone: "Europe/Bratislava"},
		{name: "six fields with seconds", spec: "0 0 3 * * *"},
		{name: "six fields sub-minute step", spec: "*/30 * * * * *"},
		{name: "six fields with timezone", spec: "*/30 * * * * *", timezone: "Europe/Bratislava"},
		{name: "four fields", spec: "* * * *", wantErr: true},
		{name: "seven fields", spec: "0 0 0 3 * * *", wantErr: true},
		{name: "out of range minute", spec: "61 * * * *", wantErr: true},
		{name: "out of range second", spec: "61 * * * * *", wantErr: true},
		{name: "garbage", spec: "every day at noon", wantErr: true},
		{name: "bad timezone", spec: "0 3 * * *", timezone: "Mars/Olympus", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.spec, tt.timezone)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSupersetOfStandardParser guards the contract that cronspec's grammar is a
// superset of robfig's standard parser — the one cron.New would use without an
// explicit WithParser. Every spec the standard parser accepts, cronspec must
// also accept (no 5-field regression); cronspec additionally accepts a leading
// seconds field. If ParseOptions ever dropped a standard option, validation and
// scheduling would reject specs the userbase already relies on.
func TestSupersetOfStandardParser(t *testing.T) {
	specs := []string{
		"0 3 * * *",
		"*/5 * * * *",
		"@hourly",
		"@daily",
		"@every 90m",
		"CRON_TZ=UTC 0 3 * * *",
		"* * * *",
		"61 * * * *",
		"nonsense",
	}
	for _, spec := range specs {
		_, oursErr := NewParser().Parse(spec)
		_, stdErr := cron.ParseStandard(spec)
		if stdErr == nil {
			require.NoError(t, oursErr,
				"cronspec rejected a spec the standard parser accepts (5-field regression): %q", spec)
		}
	}
}

// TestAcceptsSixFieldSeconds is the additive half of the superset contract: the
// leading seconds field that the standard parser rejects, cronspec accepts.
func TestAcceptsSixFieldSeconds(t *testing.T) {
	spec := "*/30 * * * * *"

	_, oursErr := NewParser().Parse(spec)
	require.NoError(t, oursErr, "cronspec should accept a 6-field spec")

	_, stdErr := cron.ParseStandard(spec)
	require.Error(t, stdErr, "standard parser should still reject a 6-field spec")
}
