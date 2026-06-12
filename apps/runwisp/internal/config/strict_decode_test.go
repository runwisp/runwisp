// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseWire calls dec.DisallowUnknownFields() over the whole tomlConfig, so an
// unknown or misspelled key in ANY section fails the load before validation.
// config_test.go only covers a task-level unknown key; these lock the same
// guard across every top-level table.
func TestStrictDecode_UnknownKeysRejected(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{
			name: "defaults",
			toml: `[defaults]
bogus = 1

[tasks.t]
run = "echo hi"
`,
		},
		{
			name: "storage",
			toml: `[storage]
bogus = "1mb"

[tasks.t]
run = "echo hi"
`,
		},
		{
			name: "daemon",
			toml: `[daemon]
bogus = true

[tasks.t]
run = "echo hi"
`,
		},
		{
			name: "scheduler",
			toml: `[scheduler]
bogus = "UTC"

[tasks.t]
run = "echo hi"
`,
		},
		{
			name: "notify",
			toml: `[notify]
bogus = "5s"

[tasks.t]
run = "echo hi"
`,
		},
		{
			name: "notifier",
			toml: `[[notifier]]
type = "slack"
bogus = "x"

[tasks.t]
run = "echo hi"
`,
		},
		{
			name: "misspelled task key",
			toml: `[tasks.t]
run = "echo hi"
maxconcurrent = 2
`,
		},
		{
			name: "misspelled section name",
			toml: `[storagee]
max_size = "1mb"

[tasks.t]
run = "echo hi"
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeTOML(t, tt.toml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unknown key")
		})
	}
}
