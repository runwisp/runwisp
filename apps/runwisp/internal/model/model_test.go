// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateTaskName_RejectsBadInputs locks in the shared validator that
// the TOML loader, REST inputs, and the cloud-dispatch resolver all rely
// on. A drift here is a latent bug: a name accepted by TOML but rejected
// by the API (or vice versa) makes the same task unreachable from
// surfaces that should round-trip cleanly.
func TestValidateTaskName_RejectsBadInputs(t *testing.T) {
	bad := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"contains slash", "foo/bar"},
		{"contains space", "foo bar"},
		{"contains hash", "foo#bar"},
		{"too long", strings.Repeat("a", TaskNameMaxLength+1)},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			assert.Error(t, ValidateTaskName(tc.in))
		})
	}
}

func TestValidateTaskName_AcceptsGoodInputs(t *testing.T) {
	for _, name := range []string{
		"task1",
		"my-task",
		"my_task",
		"task.with.dots",
		"db:backup",
		"a",
		strings.Repeat("a", TaskNameMaxLength),
	} {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, ValidateTaskName(name))
		})
	}
}

// TestTaskNamePatternString_MatchesPattern keeps the compiled regex and
// the documented huma pattern literal in lockstep — if a future change
// edits one without the other, this catches it before it ships.
func TestTaskNamePatternString_MatchesPattern(t *testing.T) {
	assert.Equal(t, TaskNamePatternString, TaskNamePattern.String())
}

// TestDaemonInfo_JSONShapeIsLocked guards the /api/info wire shape against
// silent additions of password / credential / ephemeral fields. The struct is
// the surface a future contributor would touch when "exposing the password to
// the Web UI for convenience" — locking the marshaled key set turns that into
// an explicit test failure rather than a quiet release.
func TestDaemonInfo_JSONShapeIsLocked(t *testing.T) {
	raw, err := json.Marshal(DaemonInfo{})
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &decoded))

	want := []string{
		"auth_disabled",
		"capabilities",
		"cloud_enabled",
		"config_loaded_at",
		"config_stale",
		"external_url",
		"fingerprint",
		"port",
		"resolved_timezone",
		"scheduling_active",
		"service_managed",
		"tasks",
		"timezone_source",
		"version",
	}
	got := make([]string, 0, len(decoded))
	for k := range decoded {
		got = append(got, k)
	}
	sort.Strings(got)
	assert.Equal(t, want, got,
		"DaemonInfo's marshaled top-level keys must match the documented /api/info shape exactly")

	lower := strings.ToLower(string(raw))
	for _, banned := range []string{"password", "credential", "ephemeral"} {
		assert.NotContains(t, lower, banned,
			"DaemonInfo must not gain a %q-related field — that data belongs on the local-only credentials endpoint", banned)
	}
}

// --- ParseExecutionDef ---

func TestParseExecutionDef_EmptyInput(t *testing.T) {
	_, err := ParseExecutionDef(json.RawMessage{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty execution definition")
}

func TestParseExecutionDef_NullJSON(t *testing.T) {
	_, err := ParseExecutionDef(json.RawMessage("null"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty execution definition")
}

func TestParseExecutionDef_InvalidJSON(t *testing.T) {
	_, err := ParseExecutionDef(json.RawMessage(`{notjson`))
	require.Error(t, err)
}

func TestParseExecutionDef_UnknownType(t *testing.T) {
	_, err := ParseExecutionDef(json.RawMessage(`{"type":"magic"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported execution type")
}

func TestParseExecutionDef_Shell(t *testing.T) {
	def, err := ParseExecutionDef(json.RawMessage(`{"type":"shell","script":"echo hi"}`))
	require.NoError(t, err)
	shell, ok := def.(*ShellExecution)
	require.True(t, ok, "expected *ShellExecution")
	assert.Equal(t, "echo hi", shell.Script)
}

func TestParseExecutionDef_Container(t *testing.T) {
	def, err := ParseExecutionDef(json.RawMessage(`{"type":"container","baseImage":"alpine"}`))
	require.NoError(t, err)
	c, ok := def.(*ContainerExecution)
	require.True(t, ok, "expected *ContainerExecution")
	assert.Equal(t, "alpine", c.BaseImage)
}

func TestParseExecutionDef_HTTP(t *testing.T) {
	def, err := ParseExecutionDef(json.RawMessage(`{"type":"http","url":"https://example.com"}`))
	require.NoError(t, err)
	h, ok := def.(*HTTPExecution)
	require.True(t, ok, "expected *HTTPExecution")
	assert.Equal(t, "https://example.com", h.URL)
}

func TestParseExecutionDef_Config(t *testing.T) {
	def, err := ParseExecutionDef(json.RawMessage(`{"type":"config","taskName":"mytask"}`))
	require.NoError(t, err)
	c, ok := def.(*ConfigExecution)
	require.True(t, ok, "expected *ConfigExecution")
	assert.Equal(t, "mytask", c.TaskName)
}

// --- MarshalExecutionDef ---

func TestMarshalExecutionDef_Shell(t *testing.T) {
	raw, err := MarshalExecutionDef(&ShellExecution{Script: "echo hi"})
	require.NoError(t, err)
	s := string(raw)
	assert.Contains(t, s, `"type":"shell"`)
	assert.Contains(t, s, `"script":`)
}

func TestMarshalExecutionDef_HTTP(t *testing.T) {
	raw, err := MarshalExecutionDef(&HTTPExecution{URL: "https://example.com"})
	require.NoError(t, err)
	s := string(raw)
	assert.Contains(t, s, `"type":"http"`)
	assert.Contains(t, s, `"url":`)
}

func TestMarshalExecutionDef_Config(t *testing.T) {
	raw, err := MarshalExecutionDef(&ConfigExecution{TaskName: "mytask"})
	require.NoError(t, err)
	s := string(raw)
	assert.Contains(t, s, `"type":"config"`)
	assert.Contains(t, s, `"taskName":`)
}

func TestMarshalExecutionDef_Container(t *testing.T) {
	raw, err := MarshalExecutionDef(&ContainerExecution{BaseImage: "alpine", Script: "echo"})
	require.NoError(t, err)
	s := string(raw)
	assert.Contains(t, s, `"type":"container"`)
	assert.Contains(t, s, `"baseImage":"alpine"`)
}

func TestExecType_AllVariants(t *testing.T) {
	assert.Equal(t, "shell", (&ShellExecution{}).ExecType())
	assert.Equal(t, "container", (&ContainerExecution{}).ExecType())
	assert.Equal(t, "http", (&HTTPExecution{}).ExecType())
	assert.Equal(t, "config", (&ConfigExecution{}).ExecType())
}

// --- Task.ResolvedExecutionDef ---

func TestTask_ResolvedExecutionDef_WithExecutionDef(t *testing.T) {
	def := &ShellExecution{Script: "explicit"}
	task := &Task{ExecutionDef: def}
	got := task.ResolvedExecutionDef()
	assert.Equal(t, def, got)
}

func TestTask_ResolvedExecutionDef_WithRun(t *testing.T) {
	task := &Task{Run: "echo hi"}
	got := task.ResolvedExecutionDef()
	shell, ok := got.(*ShellExecution)
	require.True(t, ok, "expected *ShellExecution")
	assert.Equal(t, "echo hi", shell.Script)
}

func TestTask_ResolvedExecutionDef_EmptyRun(t *testing.T) {
	task := &Task{}
	got := task.ResolvedExecutionDef()
	assert.Nil(t, got)
}

// TestTaskJSON_HidesSecrets guards the invariant that secrets / secrets_file
// values never escape the daemon over JSON (API, UI, cloud). Inline env, the
// env_file path, and the secrets_file path remain visible because operators
// expect them in the UI.
func TestTaskJSON_HidesSecrets(t *testing.T) {
	task := Task{
		Name:        "demo",
		Env:         map[string]string{"VISIBLE": "ok"},
		EnvFile:     "/etc/runwisp/env.env",
		SecretsFile: "/etc/runwisp/secrets.env",
		Secrets:     map[string]string{"AWS_SECRET_KEY": "do-not-leak"},
	}
	data, err := json.Marshal(&task)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, `"env":{"VISIBLE":"ok"}`)
	assert.Contains(t, body, `"env_file":"/etc/runwisp/env.env"`)
	assert.Contains(t, body, `"secrets_file":"/etc/runwisp/secrets.env"`)
	assert.NotContains(t, body, "AWS_SECRET_KEY")
	assert.NotContains(t, body, "do-not-leak")
}
