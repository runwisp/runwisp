// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeUnknownKeySuggestions(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want []string // substrings expected in the error
	}{
		{
			name: "task key typo",
			toml: "[tasks.backup]\nrun = \"echo hi\"\ntimeuot = \"5m\"\n",
			want: []string{`unknown key "timeuot"`, `did you mean "timeout"?`},
		},
		{
			name: "cron key typo",
			toml: "[tasks.backup]\nrun = \"echo hi\"\ncorn = \"* * * * *\"\n",
			want: []string{`unknown key "corn"`, `did you mean "cron"?`},
		},
		{
			name: "table name typo",
			toml: "[taks.backup]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "taks"`, `did you mean "tasks"?`},
		},
		{
			name: "daemon key typo",
			toml: "[daemon]\nshutdwn_timeout = \"5s\"\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "shutdwn_timeout"`, `did you mean "shutdown_timeout"?`},
		},
		{
			name: "notifier key typo",
			toml: "[[notifier]]\nid = \"ops\"\ntype = \"slack\"\nwebhok_url = \"https://example.com\"\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "webhok_url"`, `did you mean "webhook_url"?`},
		},
		{
			name: "route match key typo",
			toml: "[[notification_route]]\nnotify = [\"ops\"]\n[notification_route.match]\nseverty = \"error\"\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "severty"`, `did you mean "severity"?`},
		},
		{
			name: "uppercase typo still matches",
			toml: "[tasks.backup]\nrun = \"echo hi\"\nTIMEUOT = \"5m\"\n",
			want: []string{`did you mean "timeout"?`},
		},
		{
			name: "gibberish gets no suggestion",
			toml: "[tasks.backup]\nrun = \"echo hi\"\nzzqxwy = 1\n",
			want: []string{`unknown key "zzqxwy"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decode([]byte(tt.toml))
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestDecodeUnknownKeyNoSuggestionForGibberish(t *testing.T) {
	_, err := decode([]byte("[tasks.backup]\nrun = \"echo hi\"\nzzqxwy = 1\n"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "did you mean")
}

// Free-form [compose.*] blocks decode into maps, so they never produce
// strict-mode errors — this guards that the suggestion walker doesn't panic
// on paths through them either way.
func TestComposeBlockKeysDecodeWithoutError(t *testing.T) {
	_, err := decode([]byte("[compose.app]\nfile = \"docker-compose.yml\"\nanything_goes = 1\n"))
	require.NoError(t, err)
}
