// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/model"
)

func loadProbe(t *testing.T, toml string) (svc, probe *model.Task) {
	t.Helper()
	cfgPath, _ := writePlainConfig(t, toml)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	svc = findTask(t, cfg, "api")
	require.NotNil(t, svc.HealthCheck)
	return svc, svc.HealthCheck
}

func TestHealthCheck_Defaults(t *testing.T) {
	_, probe := loadProbe(t, `[services.api]
run = "serve"

[services.api.health_check]
run = "curl -fsS localhost"
`)
	assert.Equal(t, "api.health_check", probe.Name)
	assert.Equal(t, "curl -fsS localhost", probe.Run)
	assert.Equal(t, DefaultHealthCheckCron, probe.Cron)
	assert.Equal(t, DefaultHealthCheckTimeout, probe.TimeoutValue())
	assert.Equal(t, DefaultHealthCheckRetryAttempts, probe.RetryAttempts)
	assert.Equal(t, time.Duration(0), *probe.GracefulStop, "a timed-out probe is killed outright")
	assert.True(t, probe.IsFailureReason(model.ReasonFailed, 1, false))
	assert.True(t, probe.IsFailureReason(model.ReasonTimeout, -1, false))
}

func TestHealthCheck_TaskKeysResolveLikeATask(t *testing.T) {
	_, probe := loadProbe(t, `[daemon]
timezone = "Europe/Bratislava"

[defaults]
timeout = "7s"

[services.api]
run = "serve"

[services.api.health_check]
run = "check"
cron = "*/5 * * * *"
failures = ["-timeout"]
retry_attempts = 0
retry_delay = "3s"
retry_backoff = "linear"
`)
	assert.Equal(t, "*/5 * * * *", probe.Cron)
	assert.Equal(t, "Europe/Bratislava", probe.Timezone, "the cron runs in the daemon timezone")
	assert.Equal(t, 7*time.Second, probe.TimeoutValue(), "[defaults].timeout beats the probe fallback")
	assert.False(t, probe.IsFailureReason(model.ReasonTimeout, -1, false))
	assert.Zero(t, probe.RetryAttempts, "an explicit 0 is kept")
	assert.Equal(t, 3*time.Second, *probe.RetryDelay)
	assert.Equal(t, model.BackoffLinear, probe.RetryBackoff)
}

// A host probe runs where and as the service does unless it says otherwise;
// env and secrets merge key by key: [defaults] < service < probe, each side's
// env_file beneath its inline env.
func TestHealthCheck_InheritsServiceExecKeys(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[defaults]
env = { LEVEL = "defaults", FROM_DEFAULTS = "1" }

[services.api]
run = "serve"
shell = "/bin/bash"
umask = "0027"
working_dir = "app"
env_file = "svc.env"
env = { LEVEL = "service", SVC = "1" }
secrets = { TOKEN = "svc-token" }
failures = ["-timeout"]

[services.api.health_check]
run = "check"
working_dir = "probe"
env_file = "probe.env"
env = { LEVEL = "probe" }
`,
		"svc.env":   "SVC_FILE=1\nFILE_LEVEL=service\n",
		"probe.env": "FILE_LEVEL=probe\n",
	})
	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)
	probe := findTask(t, cfg, "api").HealthCheck

	assert.Equal(t, "/bin/bash", probe.Shell)
	assert.Equal(t, "0027", probe.Umask)
	assert.Equal(t, filepath.Join(dir, "probe"), probe.WorkingDir, "resolves against the service's file")
	assert.Equal(t, map[string]string{
		"LEVEL":         "probe",
		"FROM_DEFAULTS": "1",
		"SVC":           "1",
		"SVC_FILE":      "1",
		"FILE_LEVEL":    "probe",
	}, probe.Env)
	assert.Equal(t, map[string]string{"TOKEN": "svc-token"}, probe.Secrets)
	assert.True(t, probe.IsFailureReason(model.ReasonTimeout, -1, false), "failures is not inherited from the service")
}

func TestHealthCheck_ComposeProbeInheritsNothing(t *testing.T) {
	cfg, err := Load(writeConfig(t, `[services.api]
run = "serve"
shell = "/bin/bash"
env = { SVC = "1" }

[services.api.health_check]
run = "curl -fsS localhost"
compose_file = "./docker-compose.yml"
compose_service = "web"
`))
	require.NoError(t, err)
	probe := findTask(t, cfg, "api").HealthCheck

	ce, ok := probe.ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok, "expected a compose execution, got %T", probe.ExecutionDef)
	assert.Equal(t, model.ComposeModeExec, ce.Mode)
	assert.Equal(t, "web", ce.Service)
	assert.NotEqual(t, "/bin/bash", probe.Shell)
	assert.NotContains(t, probe.Env, "SVC")
}

// A probe is validated as a unit, not as a task identity: the derived name
// "<service>.health_check" must not trip the task-name length limit.
func TestHealthCheck_LongServiceNameAccepted(t *testing.T) {
	name := strings.Repeat("a", model.TaskNameMaxLength-5)
	cfgPath, _ := writePlainConfig(t, "[services."+name+"]\nrun = \"serve\"\n[services."+name+".health_check]\nrun = \"check\"\n")
	_, err := Load(cfgPath)
	require.NoError(t, err)
}

func TestHealthCheck_Rejections(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want []string
	}{
		{
			name: "run required",
			toml: "[services.api]\nrun = \"serve\"\n[services.api.health_check]\ncron = \"@every 5s\"\n",
			want: []string{"health_check for service api: run is required"},
		},
		{
			name: "zero timeout",
			toml: "[services.api]\nrun = \"serve\"\n[services.api.health_check]\nrun = \"check\"\ntimeout = \"0s\"\n",
			want: []string{"invalid timeout for health_check of service api"},
		},
		{
			name: "zero healthy_after",
			toml: "[services.api]\nrun = \"serve\"\nhealthy_after = \"0s\"\n[services.api.health_check]\nrun = \"check\"\n",
			want: []string{"invalid healthy_after for service api"},
		},
		{
			name: "compose run mode",
			toml: "[services.api]\nrun = \"serve\"\n[services.api.health_check]\nrun = \"check\"\ncompose_file = \"x.yml\"\ncompose_service = \"web\"\ncompose_mode = \"run\"\n",
			want: []string{`compose_mode = "run" is not supported`},
		},
		{
			name: "invalid cron",
			toml: "[services.api]\nrun = \"serve\"\n[services.api.health_check]\nrun = \"check\"\ncron = \"nope\"\n",
			want: []string{"api.health_check"},
		},
		{
			name: "task-only key",
			toml: "[services.api]\nrun = \"serve\"\n[services.api.health_check]\nrun = \"check\"\nkeep_runs = 5\n",
			want: []string{`unknown key "keep_runs"`},
		},
		{
			name: "on a task",
			toml: "[tasks.t]\nrun = \"echo\"\n[tasks.t.health_check]\nrun = \"check\"\n",
			want: []string{`unknown key "health_check"`, "only valid on [services.*]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgPath, _ := writePlainConfig(t, tt.toml)
			_, err := Load(cfgPath)
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// Keys inside the probe table don't get the cross-kind hint meant for keys on
// the service itself.
func TestHealthCheck_UnknownKeyGetsNoCrossKindHint(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, "[services.api]\nrun = \"serve\"\n[services.api.health_check]\nrun = \"check\"\nrestart = \"always\"\n")
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown key "restart"`)
	assert.NotContains(t, err.Error(), "only valid on")
}

func TestHealthCheck_ProbeChangeIsAServiceChange(t *testing.T) {
	_, before := loadProbe(t, "[services.api]\nrun = \"serve\"\n[services.api.health_check]\nrun = \"check\"\n")
	_, after := loadProbe(t, "[services.api]\nrun = \"serve\"\n[services.api.health_check]\nrun = \"check\"\ncron = \"@every 1m\"\n")
	svc := func(probe *model.Task) *model.Task {
		return &model.Task{Name: "api", Kind: model.KindService, Run: "serve", HealthCheck: probe}
	}

	d := DiffTasks(tasksMap(svc(before)), tasksMap(svc(after)))

	assert.Len(t, d.Changed, 1)
}
