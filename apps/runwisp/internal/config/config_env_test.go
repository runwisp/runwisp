// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFilesInDir creates a directory with named files; returns the path to
// the first file. Useful for env_file tests that need the dotenv alongside
// the runwisp.toml.
func writeFilesInDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	return dir
}

func TestLoadEnvInline(t *testing.T) {
	t.Run("inline table form", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.greet]
run = "echo hi"
env = { TASK_FLAG = "1", LANG = "C.UTF-8" }
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		assert.Equal(t, map[string]string{"TASK_FLAG": "1", "LANG": "C.UTF-8"}, cfg.Tasks[0].Env)
	})

	t.Run("section form", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.greet]
run = "echo hi"

[tasks.greet.env]
TASK_FLAG = "1"
LANG = "C.UTF-8"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		assert.Equal(t, map[string]string{"TASK_FLAG": "1", "LANG": "C.UTF-8"}, cfg.Tasks[0].Env)
	})
}

func TestLoadEnvFileResolution(t *testing.T) {
	t.Run("relative path resolves against config dir", func(t *testing.T) {
		dir := writeFilesInDir(t, map[string]string{
			"runwisp.toml": `
[tasks.backup]
run = "/bin/true"
env_file = "secrets.env"
`,
			"secrets.env": "AWS_KEY=k1\nAWS_SECRET=s1\n",
		})
		cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		assert.Equal(t, "secrets.env", cfg.Tasks[0].EnvFile)
		assert.Equal(t, map[string]string{"AWS_KEY": "k1", "AWS_SECRET": "s1"}, cfg.Tasks[0].SecretEnv)
	})

	t.Run("absolute path is used verbatim", func(t *testing.T) {
		secretsDir := t.TempDir()
		secretsPath := filepath.Join(secretsDir, "abs.env")
		require.NoError(t, os.WriteFile(secretsPath, []byte("ABS=yes\n"), 0o600))

		dir := writeFilesInDir(t, map[string]string{
			"runwisp.toml": `
[tasks.t]
run = "/bin/true"
env_file = "` + secretsPath + `"
`,
		})
		cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
		require.NoError(t, err)
		assert.Equal(t, "yes", cfg.Tasks[0].SecretEnv["ABS"])
	})

	t.Run("missing env_file errors with resolved path", func(t *testing.T) {
		dir := writeFilesInDir(t, map[string]string{
			"runwisp.toml": `
[tasks.t]
run = "/bin/true"
env_file = "missing.env"
`,
		})
		_, err := Load(filepath.Join(dir, "runwisp.toml"))
		require.Error(t, err)
		expected := filepath.Join(dir, "missing.env")
		assert.Contains(t, err.Error(), expected)
	})
}

func TestApplyDefaultsMergesEnv(t *testing.T) {
	dir := writeFilesInDir(t, map[string]string{
		"runwisp.toml": `
[defaults]
env_file = "defaults.env"

[defaults.env]
GLOBAL = "from-defaults"
OVERRIDE_ME = "defaults-wins-without-task"

[tasks.t]
run = "echo hi"
env_file = "task.env"

[tasks.t.env]
TASK_ONLY = "from-task"
OVERRIDE_ME = "from-task"
`,
		"defaults.env": "DEFAULT_SECRET=ds\nSHARED_SECRET=defaults\n",
		"task.env":     "TASK_SECRET=ts\nSHARED_SECRET=task\n",
	})

	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	task := cfg.Tasks[0]

	// Inline env: defaults merged in, task wins on collision.
	assert.Equal(t, "from-defaults", task.Env["GLOBAL"])
	assert.Equal(t, "from-task", task.Env["TASK_ONLY"])
	assert.Equal(t, "from-task", task.Env["OVERRIDE_ME"], "task inline env must override defaults inline env")

	// SecretEnv: defaults env_file merged in, task env_file wins on collision.
	assert.Equal(t, "ds", task.SecretEnv["DEFAULT_SECRET"])
	assert.Equal(t, "ts", task.SecretEnv["TASK_SECRET"])
	assert.Equal(t, "task", task.SecretEnv["SHARED_SECRET"], "task env_file must override defaults env_file")
}

func TestValidateTaskEnv(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name: "key starts with digit",
			toml: `
[tasks.t]
run = "/bin/true"
env = { "1FOO" = "x" }
`,
			wantErr: "key \"1FOO\"",
		},
		{
			name: "key contains equals",
			toml: `
[tasks.t]
run = "/bin/true"
env = { "FOO=BAR" = "x" }
`,
			wantErr: "key \"FOO=BAR\"",
		},
		{
			name: "value contains NUL",
			// TOML basic string \u0000 escape encodes a literal NUL into the value.
			toml:    "\n[tasks.t]\nrun = \"/bin/true\"\nenv = { OK = \"ok\\u0000bad\" }\n",
			wantErr: "NUL byte",
		},
		{
			name: "value exceeds size cap",
			toml: `
[tasks.t]
run = "/bin/true"
env = { BIG = "` + strings.Repeat("x", EnvMaxValueLen+1) + `" }
`,
			wantErr: "cap is",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTOML(t, tc.toml)
			_, err := Load(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestValidateTaskEnvEntryCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("[tasks.t]\nrun = \"/bin/true\"\n[tasks.t.env]\n")
	for i := 0; i <= EnvMaxEntries; i++ {
		b.WriteString("K")
		b.WriteString(itoa(i))
		b.WriteString(" = \"v\"\n")
	}
	path := writeTOML(t, b.String())
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the cap")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
