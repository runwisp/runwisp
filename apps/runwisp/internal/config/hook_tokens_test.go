// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	validHookToken = strings.Repeat("a", minHookTokenLength)
	otherHookToken = strings.Repeat("b", minHookTokenLength)
)

// Short and long form mix in one list; ${VAR} expands inside the table too.
func TestHookTokens_ShortAndLongForm(t *testing.T) {
	t.Setenv("RW_TEST_HOOK_TOKEN", otherHookToken)
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
manual_trigger = false
hook_tokens = [
  "`+validHookToken+`",
  { token = "${RW_TEST_HOOK_TOKEN}", allow = ["stop", "run"] },
]

[services.web]
run = "sleep 1000"
hook_tokens = [{ token = "`+validHookToken+`", allow = ["restart"] }]
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err, "manual_trigger = false must not block hook tokens")

	job := findTask(t, cfg, "job").HookTokens
	require.Len(t, job, 2)
	assert.Equal(t, model.HookToken{Token: validHookToken}, job[0])
	assert.Equal(t, model.HookToken{Token: otherHookToken, Allow: []model.HookAction{model.HookStop, model.HookRun}}, job[1])
	assert.True(t, job[0].Allows(model.HookRestart), "short form grants every action")
	assert.False(t, job[1].Allows(model.HookRestart))

	web := findTask(t, cfg, "web").HookTokens
	require.Len(t, web, 1)
	assert.Equal(t, []model.HookAction{model.HookRestart}, web[0].Allow)
}

func TestHookTokens_Rejected(t *testing.T) {
	cases := map[string]struct {
		toml    string
		wantErr string
	}{
		"too short": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\nhook_tokens = [\"" + validHookToken + "\", \"short\"]\n",
			wantErr: "hook_tokens[1] for task \"job\" is 5 characters",
		},
		"whitespace": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\nhook_tokens = [\"" + validHookToken + " x\"]\n",
			wantErr: "contains whitespace",
		},
		"short token in table": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\nhook_tokens = [{ token = \"short\" }]\n",
			wantErr: "is 5 characters",
		},
		"unknown action": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\nhook_tokens = [{ token = \"" + validHookToken + "\", allow = [\"delete\"] }]\n",
			wantErr: "allows \"delete\"",
		},
		"run on a service": {
			toml:    "[services.web]\nrun = \"sleep 1000\"\nhook_tokens = [{ token = \"" + validHookToken + "\", allow = [\"run\"] }]\n",
			wantErr: "allows \"run\"",
		},
		"empty allow": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\nhook_tokens = [{ token = \"" + validHookToken + "\", allow = [] }]\n",
			wantErr: "empty allow list",
		},
		"unknown table key": {
			toml:    "[tasks.job]\nrun = \"echo hi\"\nhook_tokens = [{ token = \"" + validHookToken + "\", verbs = [\"run\"] }]\n",
			wantErr: "verbs",
		},
		"in defaults": {
			toml:    "[defaults]\nhook_tokens = [\"" + validHookToken + "\"]\n\n[tasks.job]\nrun = \"echo hi\"\n",
			wantErr: "hook_tokens",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfgPath, _ := writePlainConfig(t, tc.toml)
			_, err := Load(cfgPath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.NotContains(t, err.Error(), validHookToken, "errors must never echo a token")
		})
	}
}
