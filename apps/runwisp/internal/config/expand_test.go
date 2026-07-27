// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeRawForTest decodes TOML straight into the wire shape without running
// expandConfig, so walker tests control substitution inputs explicitly.
func decodeRawForTest(src string, raw *tomlConfig) error {
	dec := toml.NewDecoder(bytes.NewReader([]byte(src)))
	dec.DisallowUnknownFields()
	return dec.Decode(raw)
}

// fakeEnv returns a lookupEnv func backed by a plain map, so walker tests
// never read the real process environment.
func fakeEnv(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestSubstitute(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("  from-file\n"), 0o600))

	env := fakeEnv(map[string]string{
		"SET":   "value",
		"EMPTY": "",
		"A":     "1",
		"B":     "2",
	})

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "no dollar passes through", in: "plain", want: "plain"},
		{name: "set var", in: "${SET}", want: "value"},
		{name: "set-but-empty var substitutes empty", in: "x${EMPTY}y", want: "xy"},
		{name: "unset var errors with path and name", in: "${MISSING}", wantErr: "tasks.backup.cron: environment variable MISSING is not set"},
		{name: "mid-string substitution", in: "pre-${SET}-post", want: "pre-value-post"},
		{name: "multiple substitutions", in: "${A}+${B}", want: "1+2"},
		{name: "escape produces literal", in: "$${SET}", want: "${SET}"},
		{name: "escape mid-string", in: "a$${b}c", want: "a${b}c"},
		{name: "lone dollar is literal", in: "cost: $5", want: "cost: $5"},
		{name: "trailing dollar is literal", in: "100$", want: "100$"},
		{name: "unterminated brace errors", in: "${SET", wantErr: "unterminated ${"},
		{name: "empty ref errors", in: "${}", wantErr: "empty ${} substitution"},
		{name: "file ref trims space", in: "${file:secret.txt}", want: "from-file"},
		{name: "file ref mid-string", in: "token=${file:secret.txt}!", want: "token=from-file!"},
		{name: "file ref missing errors", in: "${file:nope.txt}", wantErr: "read ${file:nope.txt}"},
		{name: "file ref empty path errors", in: "${file:}", wantErr: "${file:} has no path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &expander{baseDir: dir, lookupEnv: env}
			got, err := e.substitute(tc.in, "tasks.backup.cron")
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSubstitute_FilePathResolution(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "abs.txt")
	require.NoError(t, os.WriteFile(abs, []byte("absolute"), 0o600))

	e := &expander{baseDir: t.TempDir(), lookupEnv: fakeEnv(nil)}
	got, err := e.substitute("${file:"+abs+"}", "p")
	require.NoError(t, err)
	assert.Equal(t, "absolute", got, "absolute paths bypass baseDir")
}

func TestExpandConfig_WalksWireStructs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hook.url"), []byte("https://hooks.example/XYZ\n"), 0o600))

	src := `
[defaults.env]
SHARED = "${REGION}"

[tasks.backup]
cron = "${BACKUP_CRON}"
description = "backs up ${REGION}"
run = "echo ${REGION} stays literal"
env = { DEPLOY_ENV = "${REGION}" }
secrets = { TOKEN = "${file:hook.url}" }

[services.worker]
run = "worker --region ${REGION}"
instances = 1

[[notifier]]
id = "ops"
type = "slack"
webhook_url = "${file:hook.url}"
`
	var raw tomlConfig
	require.NoError(t, decodeRawForTest(src, &raw))

	env := fakeEnv(map[string]string{
		"REGION":      "eu-1",
		"BACKUP_CRON": "0 3 * * *",
	})
	require.NoError(t, expandConfig(&raw, dir, env))

	task := raw.Tasks["backup"]
	assert.Equal(t, "0 3 * * *", task.Cron)
	assert.Equal(t, "backs up eu-1", task.Description)
	assert.Equal(t, "echo ${REGION} stays literal", task.Run, "run is exempt from substitution")
	assert.Equal(t, "eu-1", task.Env["DEPLOY_ENV"])
	assert.Equal(t, "https://hooks.example/XYZ", task.Secrets["TOKEN"])

	svc := raw.Services["worker"]
	assert.Equal(t, "worker --region ${REGION}", svc.Run, "service run is exempt too")

	assert.Equal(t, "eu-1", raw.Defaults.Env["SHARED"])
	assert.Equal(t, "https://hooks.example/XYZ", raw.Notifiers[0].WebhookURL)
}

func TestExpandConfig_MapKeysStayLiteral(t *testing.T) {
	src := `
[tasks."backup"]
run = "x"
env = { KEY_NAME = "${V}" }
`
	var raw tomlConfig
	require.NoError(t, decodeRawForTest(src, &raw))
	require.NoError(t, expandConfig(&raw, t.TempDir(), fakeEnv(map[string]string{"V": "v"})))

	task, ok := raw.Tasks["backup"]
	require.True(t, ok, "task map key must stay literal")
	_, ok = task.Env["KEY_NAME"]
	assert.True(t, ok, "env map key must stay literal")
	assert.Equal(t, "v", task.Env["KEY_NAME"])
}

func TestExpandConfig_ComposeBlockStrings(t *testing.T) {
	src := `
[compose.app]
file = "${COMPOSE_FILE}"
profiles = ["${PROFILE}"]

[compose.app.web]
description = "web ${REGION}"
env = { MODE = "${MODE}" }
`
	var raw tomlConfig
	require.NoError(t, decodeRawForTest(src, &raw))

	env := fakeEnv(map[string]string{
		"COMPOSE_FILE": "stack.yaml",
		"PROFILE":      "prod",
		"REGION":       "eu-1",
		"MODE":         "fast",
	})
	require.NoError(t, expandConfig(&raw, t.TempDir(), env))

	block := raw.Compose["app"]
	assert.Equal(t, "stack.yaml", block["file"])
	assert.Equal(t, []any{"prod"}, block["profiles"])
	web, ok := block["web"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "web eu-1", web["description"])
	envMap, ok := web["env"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "fast", envMap["MODE"])
}

func TestExpandConfig_ErrorNamesNotifierPath(t *testing.T) {
	src := `
[[notifier]]
id = "ops"
type = "slack"
webhook_url = "${MISSING_HOOK}"
`
	var raw tomlConfig
	require.NoError(t, decodeRawForTest(src, &raw))

	err := expandConfig(&raw, t.TempDir(), fakeEnv(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notifier[0].webhook_url")
	assert.Contains(t, err.Error(), "MISSING_HOOK")
}

func TestExpandConfig_ErrorNamesTaskPath(t *testing.T) {
	src := `
[tasks.backup]
run = "x"
cron = "${BACKUP_CRON}"
`
	var raw tomlConfig
	require.NoError(t, decodeRawForTest(src, &raw))

	err := expandConfig(&raw, t.TempDir(), fakeEnv(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tasks.backup.cron: environment variable BACKUP_CRON is not set")
}

// TestLoad_SubstitutionEndToEnd exercises the full Load path: ${VAR} and
// ${file:...} written in runwisp.toml land expanded in the final Config,
// including notifier credentials (replacing the deleted _env/_file sources).
func TestLoad_SubstitutionEndToEnd(t *testing.T) {
	t.Setenv("RUNWISP_TEST_E2E_CRON", "0 3 * * *")
	t.Setenv("RUNWISP_TEST_E2E_TOKEN", "tg-token")
	dir := writeFilesInDir(t, map[string]string{
		"runwisp.toml": `
[scheduler]
timezone = "UTC"

[tasks.backup]
cron = "${RUNWISP_TEST_E2E_CRON}"
run = "backup.sh ${NOT_EXPANDED}"

[[notifier]]
id = "ops"
type = "slack"
webhook_url = "${file:hook.url}"

[[notifier]]
id = "oncall"
type = "telegram"
bot_token = "${RUNWISP_TEST_E2E_TOKEN}"
chat_id = "-1001"
`,
		"hook.url": "https://hooks.slack.test/T/B/Z\n",
	})

	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)

	require.Len(t, cfg.Tasks, 1)
	assert.Equal(t, "0 3 * * *", cfg.Tasks[0].Cron)
	assert.Equal(t, "backup.sh ${NOT_EXPANDED}", cfg.Tasks[0].Run)

	require.Len(t, cfg.Notify.Notifiers, 2)
	assert.Equal(t, "https://hooks.slack.test/T/B/Z", cfg.Notify.Notifiers[0].WebhookURL)
	assert.Equal(t, "tg-token", cfg.Notify.Notifiers[1].BotToken)
}

func TestLoad_SubstitutionUnsetVarFailsLoad(t *testing.T) {
	dir := writeFilesInDir(t, map[string]string{
		"runwisp.toml": `
[tasks.backup]
cron = "${RUNWISP_TEST_DEFINITELY_UNSET}"
run = "x"
`,
	})
	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tasks.backup.cron")
	assert.Contains(t, err.Error(), "RUNWISP_TEST_DEFINITELY_UNSET")
}
