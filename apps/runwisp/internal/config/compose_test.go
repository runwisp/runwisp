// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig stages a runwisp.toml + an adjacent compose file under a
// temp dir and returns the path to the toml. Tests use this to exercise
// the full Load pipeline including compose expansion.
func writeConfig(t *testing.T, toml string) string {
	t.Helper()
	dir := t.TempDir()
	composeBytes, err := os.ReadFile("testdata/basic-compose.yml")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"), composeBytes, 0644))
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(toml), 0644))
	return cfgPath
}

func TestComposeExpansion_AutoDiscoveryImportsAllServices(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]
`))
	require.NoError(t, err)

	names := taskNames(cfg)
	assert.ElementsMatch(t, []string{"myapp.web", "myapp.worker", "myapp.db"}, names)

	web := findTask(t, cfg, "myapp.web")
	assert.Equal(t, model.KindService, web.Kind)
	assert.Equal(t, model.RestartOnFailure, web.Restart)
	assert.Equal(t, "myapp", web.Group)
	assert.Equal(t, 12*time.Second, web.GracefulStop, "compose stop_grace_period propagates")

	ce, ok := web.ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok)
	assert.Equal(t, "web", ce.Service)
	assert.Equal(t, model.ComposeModeServices, ce.Mode)
	assert.Equal(t, "myapp", ce.ProjectName)
	assert.Equal(t, model.ComposePullMissing, ce.Pull)
}

func TestComposeExpansion_IncludeFiltersDown(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]
include = ["web"]
`))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"myapp.web"}, taskNames(cfg))
}

func TestComposeExpansion_ExcludeDropsService(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]
exclude = ["db"]
`))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"myapp.web", "myapp.worker"}, taskNames(cfg))
}

func TestComposeExpansion_IncludeAndExcludeAreMutuallyExclusive(t *testing.T) {
	_, err := Load(writeConfig(t, `[compose.myapp]
include = ["web"]
exclude = ["db"]
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestComposeExpansion_UnknownIncludeServiceReported(t *testing.T) {
	_, err := Load(writeConfig(t, `[compose.myapp]
include = ["webb"]
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webb")
	assert.Contains(t, err.Error(), "not in the compose file")
}

func TestComposeExpansion_PerServiceOverrideApplies(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]

[compose.myapp.web]
restart   = "always"
instances = 2
env       = { LOG_LEVEL = "info" }
`))
	require.NoError(t, err)

	web := findTask(t, cfg, "myapp.web")
	assert.Equal(t, model.RestartAlways, web.Restart)
	assert.Equal(t, 2, web.Instances)
	assert.Equal(t, "info", web.Env["LOG_LEVEL"])

	// non-overridden services retain compose defaults
	worker := findTask(t, cfg, "myapp.worker")
	assert.Equal(t, model.RestartOnFailure, worker.Restart)
	assert.Equal(t, 1, worker.Instances)
}

func TestComposeExpansion_PerServiceOverrideServiceKnobs(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]

[compose.myapp.web]
stop_signal   = "SIGINT"
exit_codes    = [0, 42]
start_retries = 1
priority      = 5
autostart     = false
`))
	require.NoError(t, err)

	web := findTask(t, cfg, "myapp.web")
	assert.Equal(t, "SIGINT", web.StopSignal)
	assert.Equal(t, []int{0, 42}, web.ExitCodes)
	assert.Equal(t, 1, web.StartRetries)
	assert.Equal(t, 5, web.Priority)
	assert.False(t, web.Autostart)

	// A non-overridden service keeps the compose-import defaults.
	worker := findTask(t, cfg, "myapp.worker")
	assert.True(t, worker.Autostart)
	assert.Equal(t, 0, worker.Priority)
}

func TestComposeExpansion_StackModeProducesSingleTask(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]
mode = "stack"
`))
	require.NoError(t, err)
	assert.Equal(t, []string{"myapp"}, taskNames(cfg))

	ce, ok := findTask(t, cfg, "myapp").ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok)
	assert.Equal(t, model.ComposeModeStack, ce.Mode)
	assert.Empty(t, ce.Service)
}

func TestComposeExpansion_StackRejectsPerServiceOverride(t *testing.T) {
	_, err := Load(writeConfig(t, `[compose.myapp]
mode = "stack"

[compose.myapp.web]
restart = "always"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stack")
}

func TestComposeExpansion_NameFormatMustContainService(t *testing.T) {
	_, err := Load(writeConfig(t, `[compose.myapp]
name_format = "static"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{service}")
}

func TestComposeExpansion_OverrideForUnknownServiceRejected(t *testing.T) {
	_, err := Load(writeConfig(t, `[compose.myapp]

[compose.myapp.webb]
restart = "always"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webb")
}

func TestComposeExpansion_PullValueValidated(t *testing.T) {
	_, err := Load(writeConfig(t, `[compose.myapp]
pull = "sometimes"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pull")
}

func TestComposeExpansion_RunMutuallyExclusiveWithComposeFile(t *testing.T) {
	cfgPath := writeConfig(t, `[services.svc]
run = "echo hi"
compose_file = "./docker-compose.yml"
compose_service = "web"
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both")
}

func TestComposeExpansion_ServiceLevelComposeFileBuildsExecutionDef(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[services.api]
compose_file    = "./docker-compose.yml"
compose_service = "web"
`))
	require.NoError(t, err)

	api := findTask(t, cfg, "api")
	ce, ok := api.ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok)
	assert.Equal(t, "web", ce.Service)
	assert.Equal(t, "api", ce.ProjectName)
}

// TestComposeExpansion_PathsAreAbsoluteUnderRelativeConfigDir reproduces the
// path-doubling bug seen when the daemon is launched with a relative -c path:
// the compose file path must be absolute on the ComposeExecution, because at
// exec time `docker compose -f <file>` runs with cwd = working_dir. A relative
// file is then resolved twice (baseDir at load, working_dir at exec) and the
// prefix doubles. Covers both the [compose.*] block and the standalone
// [services.*] compose_file path. Uses a RELATIVE config path on purpose —
// with an absolute one the joined path is already absolute and the bug hides.
func TestComposeExpansion_PathsAreAbsoluteUnderRelativeConfigDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "cfgdir")
	require.NoError(t, os.MkdirAll(sub, 0755))
	composeBytes, err := os.ReadFile("testdata/basic-compose.yml")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sub, "docker-compose.yml"), composeBytes, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "runwisp.toml"), []byte(`[compose.block]
file = "./docker-compose.yml"

[services.standalone]
compose_file    = "./docker-compose.yml"
compose_service = "web"
`), 0644))

	t.Chdir(root) // auto-restored; Load now sees baseDir = "cfgdir" (relative)
	cfg, err := Load(filepath.Join("cfgdir", "runwisp.toml"))
	require.NoError(t, err)

	wantFile := filepath.Join(sub, "docker-compose.yml")
	for _, name := range []string{"block.web", "standalone"} {
		ce, ok := findTask(t, cfg, name).ExecutionDef.(*model.ComposeExecution)
		require.True(t, ok, "%s should be a ComposeExecution", name)
		assert.True(t, filepath.IsAbs(ce.File), "%s: File must be absolute, got %q", name, ce.File)
		assert.Equal(t, wantFile, ce.File, "%s: File resolved to the staged compose file", name)
		assert.Equal(t, sub, ce.WorkingDir, "%s: WorkingDir is the compose file's directory", name)
		assert.FileExists(t, ce.File, "%s: resolved File must exist", name)
	}
}

func TestComposeExpansion_ComposeServiceWithoutComposeFileRejected(t *testing.T) {
	_, err := Load(writeConfig(t, `[services.api]
compose_service = "web"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compose_service")
}

func TestComposeExpansion_NoComposeFileFoundProducesClearError(t *testing.T) {
	// Pass a config dir that does NOT contain a compose file.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`[compose.myapp]
`), 0644))
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no compose file")
}

// --- test helpers ---

func taskNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Tasks))
	for i := range cfg.Tasks {
		names = append(names, cfg.Tasks[i].Name)
	}
	return names
}

func findTask(t *testing.T, cfg *Config, name string) *model.Task {
	t.Helper()
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Name == name {
			return &cfg.Tasks[i]
		}
	}
	t.Fatalf("task %q not found in cfg (have: %v)", name, taskNames(cfg))
	return nil
}
