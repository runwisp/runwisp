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
		{name: "four fields", spec: "* * * *", wantErr: true},
		{name: "six fields", spec: "0 0 3 * * *", wantErr: true},
		{name: "out of range minute", spec: "61 * * * *", wantErr: true},
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

// TestParityWithStandardParser guards the contract that cronspec's grammar is
// exactly robfig's standard parser — the one cron.New would use without an
// explicit WithParser. If ParseOptions ever drifts, validation and scheduling
// would accept different specs.
func TestParityWithStandardParser(t *testing.T) {
	specs := []string{
		"0 3 * * *",
		"*/5 * * * *",
		"@hourly",
		"@daily",
		"@every 90m",
		"CRON_TZ=UTC 0 3 * * *",
		"* * * *",
		"61 * * * *",
		"0 0 3 * * *",
		"nonsense",
	}
	for _, spec := range specs {
		_, oursErr := NewParser().Parse(spec)
		_, stdErr := cron.ParseStandard(spec)
		require.Equal(t, stdErr == nil, oursErr == nil,
			"grammar drift for %q: cronspec err=%v, standard err=%v", spec, oursErr, stdErr)
	}
}
