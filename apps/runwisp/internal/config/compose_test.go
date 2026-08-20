// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	assert.Equal(t, model.ComposeModeRun, ce.Mode)
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
restart_attempts = 1
priority      = 5
autostart     = false
`))
	require.NoError(t, err)

	web := findTask(t, cfg, "myapp.web")
	assert.Equal(t, "SIGINT", web.StopSignal)
	assert.Equal(t, []int{0, 42}, web.ExitCodes)
	assert.Equal(t, 1, web.RestartAttempts)
	assert.Equal(t, 5, web.Priority)
	assert.False(t, web.Autostart)

	// A non-overridden service keeps the compose-import defaults.
	worker := findTask(t, cfg, "myapp.worker")
	assert.True(t, worker.Autostart)
	assert.Equal(t, 0, worker.Priority)
}

func TestComposeExpansion_StackModeProducesSingleTask(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]
import = "stack"
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
import = "stack"

[compose.myapp.web]
restart = "always"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stack")
}

func TestComposeExpansion_BlockDefaultsApplyToAllServices(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]

[compose.myapp.defaults]
restart = "always"
`))
	require.NoError(t, err)

	for _, name := range []string{"myapp.web", "myapp.worker", "myapp.db"} {
		assert.Equal(t, model.RestartAlways, findTask(t, cfg, name).Restart, "%s inherits the block default", name)
	}
}

func TestComposeExpansion_PerServiceOverrideBeatsBlockDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]

[compose.myapp.defaults]
restart = "always"

[compose.myapp.web]
restart = "on_failure"
`))
	require.NoError(t, err)

	assert.Equal(t, model.RestartOnFailure, findTask(t, cfg, "myapp.web").Restart, "per-service override wins")
	assert.Equal(t, model.RestartAlways, findTask(t, cfg, "myapp.worker").Restart, "others keep the block default")
}

func TestComposeExpansion_BlockDefaultsNonPolicyKnobs(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[compose.myapp]

[compose.myapp.defaults]
restart_attempts = 3
exit_codes       = [0, 2]
`))
	require.NoError(t, err)

	for _, name := range []string{"myapp.web", "myapp.worker", "myapp.db"} {
		task := findTask(t, cfg, name)
		assert.Equal(t, 3, task.RestartAttempts, "%s inherits restart_attempts", name)
		assert.Equal(t, []int{0, 2}, task.ExitCodes, "%s inherits exit_codes", name)
	}
}

func TestComposeExpansion_BlockDefaultsNotifyDesugarsPerService(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[notifiers.slack-prod]
type = "slack"
webhook_url = "https://example/hook"

[compose.myapp]

[compose.myapp.defaults]
notify_on_failure = ["slack-prod"]
`))
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	for _, name := range []string{"myapp.web", "myapp.worker", "myapp.db"} {
		route := findRoute(t, cfg, name, "run.failed")
		assert.Contains(t, route.NotifierID, "slack-prod", "%s gets the block-default failure route", name)
	}
}

func TestComposeExpansion_ServiceNamedDefaultsRejected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(`services:
  defaults:
    image: alpine
`), 0644))
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`[compose.myapp]
`), 0644))

	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "defaults")
	assert.Contains(t, err.Error(), "rename")
}

func TestComposeExpansion_StackRejectsBlockDefaults(t *testing.T) {
	_, err := Load(writeConfig(t, `[compose.myapp]
import = "stack"

[compose.myapp.defaults]
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

// `run` alongside compose_file used to be a hard error. It now selects exec
// mode, which is the whole point of the feature — so this pins the new
// resolution rather than the old rejection.
func TestComposeExpansion_RunWithComposeFileDefaultsToExec(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[tasks.artisan]
cron            = "* * * * *"
run             = "php artisan schedule:run"
compose_file    = "./docker-compose.yml"
compose_service = "web"
`))
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)

	ce, ok := cfg.Tasks[0].ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok, "expected a compose execution, got %T", cfg.Tasks[0].ExecutionDef)
	assert.Equal(t, model.ComposeModeExec, ce.Mode)
	assert.Equal(t, "php artisan schedule:run", ce.Command)
	assert.Equal(t, "web", ce.Service)
}

// Exec mode must not stamp a project name. RunWisp's own task name is the right
// namespace for a container it creates, but exec targets someone else's
// container: passing `-p <task name>` made compose search a project that does
// not exist and report the service as not running for every single run.
func TestComposeExpansion_ExecModeLeavesProjectToCompose(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[tasks.artisan]
cron            = "* * * * *"
run             = "php artisan schedule:run"
compose_file    = "./docker-compose.yml"
compose_service = "app"
`))
	require.NoError(t, err)

	ce, ok := cfg.Tasks[0].ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok)
	assert.Empty(t, ce.ProjectName,
		"exec mode must let compose resolve the project the way `docker compose -f … exec` does")
}

// Run mode still names the project after the unit: it creates the container, so
// it owns the namespace and the deterministic --name built from it.
func TestComposeExpansion_RunModeKeepsProjectName(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[tasks.nightly-backup]
cron            = "0 3 * * *"
compose_file    = "./docker-compose.yml"
compose_service = "backup"
`))
	require.NoError(t, err)

	ce, ok := cfg.Tasks[0].ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok)
	assert.Equal(t, "nightly-backup", ce.ProjectName)
}

// No `run` keeps the historical behaviour: a fresh container running the
// service's own compose-declared command.
func TestComposeExpansion_ComposeFileWithoutRunStaysServicesMode(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[tasks.backup]
cron            = "0 3 * * *"
compose_file    = "./docker-compose.yml"
compose_service = "backup"
`))
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)

	ce, ok := cfg.Tasks[0].ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok)
	assert.Equal(t, model.ComposeModeRun, ce.Mode)
	assert.Empty(t, ce.Command)
}

// The combination the old error existed to prevent is still rejected, but now
// only when the operator explicitly asks for the mode that has nowhere to put a
// command — and the message names the fix.
func TestComposeExpansion_RunWithExplicitRunModeRejected(t *testing.T) {
	_, err := Load(writeConfig(t, `[tasks.artisan]
cron            = "* * * * *"
run             = "php artisan schedule:run"
compose_file    = "./docker-compose.yml"
compose_service = "web"
compose_mode    = "run"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compose_mode")
	assert.Contains(t, err.Error(), model.ComposeModeExec)
}

// Every param kind loads on a compose task in either mode. In run mode the argv
// kinds take docker's COMMAND slot rather than appending — a documented
// difference, not a config error.
func TestComposeExpansion_ParamsAcceptedInBothModes(t *testing.T) {
	for _, param := range []string{
		`{ env = "TARGET", default = "all" }`,
		`{ arg = "target", default = "all" }`,
		`{ option = "--target", default = "all" }`,
		`{ flag = "--verbose" }`,
	} {
		runMode, err := Load(writeConfig(t, `[tasks.report]
cron            = "0 3 * * *"
compose_file    = "./docker-compose.yml"
compose_service = "app"
params          = [ `+param+` ]
`))
		require.NoError(t, err, "run mode should accept %s", param)
		require.Len(t, runMode.Tasks[0].Parameters, 1)

		execMode, err := Load(writeConfig(t, `[tasks.report]
cron            = "0 3 * * *"
run             = "./report.sh"
compose_file    = "./docker-compose.yml"
compose_service = "app"
compose_mode    = "exec"
params          = [ `+param+` ]
`))
		require.NoError(t, err, "exec mode should accept %s", param)
		require.Len(t, execMode.Tasks[0].Parameters, 1)
	}
}

func TestComposeExpansion_ExecModeWithoutRunRejected(t *testing.T) {
	_, err := Load(writeConfig(t, `[tasks.artisan]
cron            = "* * * * *"
compose_file    = "./docker-compose.yml"
compose_service = "web"
compose_mode    = "exec"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a command")
}

func TestComposeExpansion_InvalidComposeModeRejected(t *testing.T) {
	_, err := Load(writeConfig(t, `[tasks.artisan]
cron            = "* * * * *"
run             = "true"
compose_file    = "./docker-compose.yml"
compose_mode    = "attach"
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid compose_mode")
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

func TestComposeExpansion_PerServiceNotifyDesugarsToRoutes(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[notifiers.slack-prod]
type = "slack"
webhook_url = "https://example/hook"

[notifiers.slack-ok]
type = "slack"
webhook_url = "https://example/ok"

[compose.myapp]

[compose.myapp.web]
notify_on_failure = ["slack-prod"]
notify_on_success = ["slack-ok"]
`))
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	failure := findRoute(t, cfg, "myapp.web", "run.failed")
	// notify_on_failure fans out across all failure kinds plus the appended
	// global default channel (inapp).
	assert.ElementsMatch(t,
		[]string{"run.failed", "run.timeout", "run.crashed", "run.missed", "service.fatal"},
		failure.Kinds)
	assert.ElementsMatch(t, []string{"slack-prod", "inapp"}, failure.NotifierID)

	success := findRoute(t, cfg, "myapp.web", "run.succeeded")
	assert.ElementsMatch(t, []string{"run.succeeded"}, success.Kinds)
	assert.ElementsMatch(t, []string{"slack-ok", "inapp"}, success.NotifierID)

	// A service without notify overrides produces no task-scoped route.
	for _, r := range cfg.Notify.Routes {
		assert.NotEqual(t, "myapp.worker", r.TaskGlob, "unconfigured service must not get a route")
	}
}

func TestComposeExpansion_PerServiceNotifyInlineTokenMaterialisesNotifier(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[notifiers.slack-prod]
type = "slack"
webhook_url = "https://example/hook"
channel = "#default"

[compose.myapp]

[compose.myapp.web]
notify_on_failure = ["slack-prod:#alerts"]
`))
	require.NoError(t, err)
	require.NoError(t, Validate(cfg))

	failure := findRoute(t, cfg, "myapp.web", "run.failed")
	assert.Contains(t, failure.NotifierID, "slack-prod:#alerts")

	// The late inline-token pass must materialise a synthetic notifier whose
	// channel is the override, cloned from the parent's credentials.
	var synth *NotifierSpec
	for i := range cfg.Notify.Notifiers {
		if cfg.Notify.Notifiers[i].ID == "slack-prod:#alerts" {
			synth = &cfg.Notify.Notifiers[i]
		}
	}
	require.NotNil(t, synth, "inline override token must materialise a synthetic notifier")
	assert.Equal(t, "#alerts", synth.SlackChannel)
	assert.Equal(t, "https://example/hook", synth.WebhookURL, "synthetic notifier inherits parent credentials")
}

func TestComposeExpansion_PerServiceNotifyUnknownNotifierRejected(t *testing.T) {
	_, err := Load(writeConfig(t, `[compose.myapp]

[compose.myapp.web]
notify_on_failure = ["ghost"]
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ghost")
}

// --- test helpers ---

// findRoute returns the notify route matching taskGlob that fires on kind.
// Fails the test when no such route exists.
func findRoute(t *testing.T, cfg *Config, taskGlob, kind string) NotificationRoute {
	t.Helper()
	for _, r := range cfg.Notify.Routes {
		if r.TaskGlob != taskGlob {
			continue
		}
		for _, k := range r.Kinds {
			if k == kind {
				return r
			}
		}
	}
	t.Fatalf("no notify route for task %q with kind %q", taskGlob, kind)
	return NotificationRoute{}
}

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
