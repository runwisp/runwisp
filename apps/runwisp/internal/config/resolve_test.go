// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRunwisp writes toml to a temp runwisp.toml and returns its path.
func writeRunwisp(t *testing.T, toml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte(toml), 0o644))
	return path
}

func TestLoad_TypedFieldResolvesFromEnv(t *testing.T) {
	t.Setenv("RW_TEST_TIMEOUT", "45s")
	t.Setenv("RW_TEST_CRON", "*/5 * * * *")
	path := writeRunwisp(t, `
[tasks.build]
run = "echo hi"
cron = "${RW_TEST_CRON}"
timeout = "${RW_TEST_TIMEOUT}"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	assert.Equal(t, 45*time.Second, cfg.Tasks[0].Timeout, "typed field resolves then parses")
	assert.Equal(t, "*/5 * * * *", cfg.Tasks[0].Cron)
}

func TestLoad_TypedFieldMissingVarFails(t *testing.T) {
	require.NoError(t, os.Unsetenv("RW_TEST_MISSING_TIMEOUT"))
	path := writeRunwisp(t, `
[tasks.build]
run = "echo hi"
timeout = "${RW_TEST_MISSING_TIMEOUT}"
`)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RW_TEST_MISSING_TIMEOUT")
}

func TestLoad_TypedFieldDefaultFallback(t *testing.T) {
	require.NoError(t, os.Unsetenv("RW_TEST_DEFAULT_TIMEOUT"))
	path := writeRunwisp(t, `
[tasks.build]
run = "echo hi"
timeout = "${RW_TEST_DEFAULT_TIMEOUT:-30s}"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.Tasks[0].Timeout)
}

func TestLoad_FreeFormEnvKeptRaw(t *testing.T) {
	t.Setenv("RW_TEST_SECRET", "s3cr3t-value")
	path := writeRunwisp(t, `
[tasks.build]
run = "echo $API_KEY"

[tasks.build.env]
API_KEY = "${RW_TEST_SECRET}"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	// config keeps the raw placeholder — the resolved secret never lands here.
	assert.Equal(t, "${RW_TEST_SECRET}", cfg.Tasks[0].Env["API_KEY"])
}

func TestLoad_FreeFormEnvMissingVarFails(t *testing.T) {
	require.NoError(t, os.Unsetenv("RW_TEST_ENV_MISSING"))
	path := writeRunwisp(t, `
[tasks.build]
run = "true"

[tasks.build.env]
API_KEY = "${RW_TEST_ENV_MISSING}"
`)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RW_TEST_ENV_MISSING")
}

func TestCollectHiddenSecrets_RevealClassification(t *testing.T) {
	t.Setenv("RW_HIDDEN", "hidden-value-xyz")
	t.Setenv("RW_SHOWN", "shown-value-abc")
	path := writeRunwisp(t, `
[tasks.build]
run = "true"

[tasks.build.env]
SECRET = "${RW_HIDDEN}"
LEVEL = "${RW_SHOWN}"
PLAIN = "literal"
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	reveal := map[string]bool{"RW_SHOWN": true}
	hidden, err := CollectHiddenSecrets(cfg, "", reveal)
	require.NoError(t, err)
	assert.Contains(t, hidden, "hidden-value-xyz", "unrevealed var resolves into the hidden set")
	assert.NotContains(t, hidden, "shown-value-abc", "revealed var is not hidden")
	assert.NotContains(t, hidden, "literal", "plain literal is not a secret")
}

func TestCollectHiddenSecrets_FileAlwaysHidden(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("file-secret-value\n"), 0o600))
	path := writeRunwisp(t, `
[tasks.build]
run = "true"

[tasks.build.env]
TOKEN = "${file:secret.txt}"
`)
	cfg, err := LoadWithDataDir(path, dir)
	require.NoError(t, err)

	// Even if every name were revealed, a file reference can never be revealed.
	hidden, err := CollectHiddenSecrets(cfg, dir, map[string]bool{"secret.txt": true})
	require.NoError(t, err)
	assert.Contains(t, hidden, "file-secret-value")
}

func TestLoad_RevealVarsParsed(t *testing.T) {
	path := writeRunwisp(t, `
[daemon]
reveal_vars = ["LOG_LEVEL", "BASE_URL"]

[tasks.build]
run = "true"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"LOG_LEVEL", "BASE_URL"}, cfg.Daemon.RevealVars)
}
