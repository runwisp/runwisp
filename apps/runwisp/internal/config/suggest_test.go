// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
			toml: "[notifiers.ops]\ntype = \"slack\"\nwebhok_url = \"https://example.com\"\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "webhok_url"`, `did you mean "webhook_url"?`},
		},
		{
			name: "route match key typo",
			toml: "[[route]]\nnotifiers = [\"ops\"]\n[route.match]\nfailur = true\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "failur"`, `did you mean "failure"?`},
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
			_, err := decode([]byte(tt.toml), "")
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestDecodeMisplacedKeySectionHints(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want []string // substrings expected in the error
	}{
		{
			name: "on_overlap in defaults is per-task",
			toml: "[defaults]\non_overlap = \"skip\"\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "on_overlap"`, "per-task", "[tasks.<name>]"},
		},
		{
			name: "timezone in defaults points at daemon",
			toml: "[defaults]\ntimezone = \"Europe/Bratislava\"\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "timezone"`, "[daemon] timezone"},
		},
		{
			name: "host in daemon points at flag",
			toml: "[daemon]\nhost = \"0.0.0.0\"\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "host"`, "--host"},
		},
		{
			name: "port in daemon points at flag",
			toml: "[daemon]\nport = 9477\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "port"`, "--port"},
		},
		{
			name: "keep_occurrences in notify points at coalesce_every",
			toml: "[notify]\nkeep_occurrences = 5\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "keep_occurrences"`, "renamed to coalesce_every"},
		},
		{
			name: "removed scheduler table points at daemon",
			toml: "[scheduler]\ntimezone = \"UTC\"\n[tasks.t]\nrun = \"echo hi\"\n",
			want: []string{`unknown key "scheduler"`, "[daemon]"},
		},
		{
			name: "restart under tasks points at services",
			toml: "[tasks.t]\nrun = \"echo hi\"\nrestart = \"always\"\n",
			want: []string{`unknown key "restart"`, "only valid on [services.*]"},
		},
		{
			name: "restart_delay under tasks points at services",
			toml: "[tasks.t]\nrun = \"echo hi\"\nrestart_delay = \"5s\"\n",
			want: []string{`unknown key "restart_delay"`, "only valid on [services.*]"},
		},
		{
			name: "instances under tasks points at services",
			toml: "[tasks.t]\nrun = \"echo hi\"\ninstances = 2\n",
			want: []string{`unknown key "instances"`, "only valid on [services.*]"},
		},
		{
			name: "depends_on under tasks points at services",
			toml: "[tasks.t]\nrun = \"echo hi\"\ndepends_on = [\"other\"]\n",
			want: []string{`unknown key "depends_on"`, "only valid on [services.*]"},
		},
		{
			name: "cron under services points at tasks",
			toml: "[services.s]\nrun = \"echo hi\"\ncron = \"* * * * *\"\n",
			want: []string{`unknown key "cron"`, "only valid on [tasks.*]"},
		},
		{
			name: "on_overlap under services points at tasks",
			toml: "[services.s]\nrun = \"echo hi\"\non_overlap = \"skip\"\n",
			want: []string{`unknown key "on_overlap"`, "only valid on [tasks.*]"},
		},
		{
			name: "params under services points at tasks",
			toml: "[services.s]\nrun = \"echo hi\"\nparams = [{env = \"X\"}]\n",
			want: []string{`unknown key "params"`, "only valid on [tasks.*]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decode([]byte(tt.toml), "")
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestDecodeUnknownKeyNoSuggestionForGibberish(t *testing.T) {
	_, err := decode([]byte("[tasks.backup]\nrun = \"echo hi\"\nzzqxwy = 1\n"), "")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "did you mean")
}

// [compose.*] blocks decode via a normal strict struct (reserved scalars plus
// a single "override" sub-table), so an unrecognised key is a parse-time
// strict-mode error with the same did-you-mean support as [tasks.*] /
// [services.*], with no more silent free-form acceptance.
func TestComposeBlockKeysDecodeStrictly(t *testing.T) {
	_, err := decode([]byte("[compose.app]\nfile = \"docker-compose.yml\"\nanything_goes = 1\n"), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown key "anything_goes"`)
}
