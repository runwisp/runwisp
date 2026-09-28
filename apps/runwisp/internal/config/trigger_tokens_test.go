// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var validTriggerToken = strings.Repeat("a", minTriggerTokenLength)

func TestTriggerTokens_LoadedFromEnv(t *testing.T) {
	t.Setenv("RW_TEST_HOOK_TOKEN", validTriggerToken)
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
trigger_tokens = ["${RW_TEST_HOOK_TOKEN}"]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{validTriggerToken}, findTask(t, cfg, "job").TriggerTokens)
}

func TestTriggerTokens_Rejected(t *testing.T) {
	cases := map[string]struct {
		toml    string
		wantErr string
	}{
		"too short": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\ntrigger_tokens = [\"" + validTriggerToken + "\", \"short\"]\n",
			wantErr: "trigger_tokens[1] for task \"job\" is 5 characters",
		},
		"whitespace": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\ntrigger_tokens = [\"" + validTriggerToken + " x\"]\n",
			wantErr: "contains whitespace",
		},
		"manual_trigger false": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\nmanual_trigger = false\ntrigger_tokens = [\"" + validTriggerToken + "\"]\n",
			wantErr: "manual_trigger = false",
		},
		"on a service": {
			toml:    "[services.web]\nrun = \"sleep 1000\"\ntrigger_tokens = [\"" + validTriggerToken + "\"]\n",
			wantErr: "trigger_tokens",
		},
		"in defaults": {
			toml:    "[defaults]\ntrigger_tokens = [\"" + validTriggerToken + "\"]\n\n[tasks.job]\nrun = \"echo hi\"\n",
			wantErr: "trigger_tokens",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfgPath, _ := writePlainConfig(t, tc.toml)
			_, err := Load(cfgPath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.NotContains(t, err.Error(), validTriggerToken, "errors must never echo a token")
		})
	}
}
