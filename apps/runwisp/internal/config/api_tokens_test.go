// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPITokens_LoadWithSubstitution(t *testing.T) {
	t.Setenv("RW_TEST_API_TOKEN", validHookToken)
	cfgPath, _ := writePlainConfig(t, `[daemon]
api_tokens = ["${RW_TEST_API_TOKEN}"]
viewer_api_tokens = ["`+otherHookToken+`"]

[tasks.job]
run = "echo hi"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, []string{validHookToken}, cfg.Daemon.APITokens)
	assert.Equal(t, []string{otherHookToken}, cfg.Daemon.ViewerAPITokens)
}

func TestAPITokens_Rejected(t *testing.T) {
	list := func(tokens ...string) string { return `["` + strings.Join(tokens, `", "`) + `"]` }
	cases := map[string]struct {
		daemon  string
		wantErr string
	}{
		"too short": {
			daemon:  "api_tokens = " + list(validHookToken, "short"),
			wantErr: "daemon.api_tokens[1] is 5 characters",
		},
		"viewer too short": {
			daemon:  "viewer_api_tokens = " + list("short"),
			wantErr: "daemon.viewer_api_tokens[0] is 5 characters",
		},
		"empty": {
			daemon:  "api_tokens = " + list(""),
			wantErr: "daemon.api_tokens[0] is 0 characters",
		},
		"whitespace": {
			daemon:  "api_tokens = " + list(validHookToken+" x"),
			wantErr: "daemon.api_tokens[0] contains whitespace",
		},
		"shaped like a session token": {
			daemon:  "viewer_api_tokens = " + list("1700000000."+validHookToken),
			wantErr: "daemon.viewer_api_tokens[0] starts like a session token",
		},
		"duplicate in one list": {
			daemon:  "api_tokens = " + list(validHookToken, validHookToken),
			wantErr: "daemon.api_tokens[1] repeats daemon.api_tokens[0]",
		},
		"duplicate across lists": {
			daemon:  "api_tokens = " + list(validHookToken) + "\nviewer_api_tokens = " + list(validHookToken),
			wantErr: "daemon.viewer_api_tokens[0] repeats daemon.api_tokens[0]",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfgPath, _ := writePlainConfig(t, "[daemon]\n"+tc.daemon+"\n\n[tasks.job]\nrun = \"echo hi\"\n")
			_, err := Load(cfgPath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.NotContains(t, err.Error(), validHookToken, "errors must never echo a token")
		})
	}
}
