// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadValidatesCron(t *testing.T) {
	valid := []struct {
		name string
		cron string
	}{
		{name: "five fields", cron: "0 3 * * *"},
		{name: "step", cron: "*/5 * * * *"},
		{name: "six fields with seconds", cron: "0 0 3 * * *"},
		{name: "six fields sub-minute step", cron: "*/30 * * * * *"},
		{name: "descriptor", cron: "@hourly"},
		{name: "every", cron: "@every 1h30m"},
	}
	for _, tt := range valid {
		t.Run("valid/"+tt.name, func(t *testing.T) {
			path := writeTOML(t, "[tasks.backup]\nrun = \"echo hi\"\ncron = \""+tt.cron+"\"\n")
			_, err := Load(path)
			assert.NoError(t, err)
		})
	}

	invalid := []struct {
		name string
		cron string
	}{
		{name: "four fields", cron: "* * * *"},
		{name: "seven fields", cron: "0 0 0 3 * * *"},
		{name: "out of range", cron: "61 * * * *"},
		{name: "garbage", cron: "every day at noon"},
	}
	for _, tt := range invalid {
		t.Run("invalid/"+tt.name, func(t *testing.T) {
			path := writeTOML(t, "[tasks.backup]\nrun = \"echo hi\"\ncron = \""+tt.cron+"\"\n")
			_, err := Load(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `invalid cron for task "backup"`)
			assert.Contains(t, err.Error(), tt.cron)
			// The hint teaches the accepted grammar without a docs lookup:
			// 5-field, optional-seconds 6-field, descriptors, and @every.
			assert.Contains(t, err.Error(), `expected 5 fields`)
			assert.Contains(t, err.Error(), `6 fields`)
			assert.Contains(t, err.Error(), `@every 1h30m`)
		})
	}
}

func TestLoadEmptyCronIsTriggerOnly(t *testing.T) {
	path := writeTOML(t, "[tasks.manual]\nrun = \"echo hi\"\n")
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	assert.Empty(t, cfg.Tasks[0].Cron)
}

// An invalid per-task timezone must surface as the friendly timezone error,
// not as a CRON_TZ parse failure from the cron validator.
func TestLoadBadTimezoneBeatsCronError(t *testing.T) {
	path := writeTOML(t, "[tasks.backup]\nrun = \"echo hi\"\ncron = \"0 3 * * *\"\ntimezone = \"Mars/Olympus\"\n")
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a valid IANA timezone")
	assert.NotContains(t, err.Error(), "invalid cron")
}
