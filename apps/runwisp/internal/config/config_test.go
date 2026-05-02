// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTask(name string) model.Task {
	return model.Task{
		Name:        name,
		Run:         "echo hello",
		Parallelism: 1,
		OnOverlap:   model.PolicyQueue,
	}
}

func writeTOML(t *testing.T, content string) string {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "config-*.toml")
	require.NoError(t, err)
	_, err = tmpfile.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())
	t.Cleanup(func() { _ = os.Remove(tmpfile.Name()) })
	return tmpfile.Name()
}

func TestLoad(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
allow_cloud_dispatch = true

[defaults]
timeout = "30m"
log_max_size = "200mb"
log_on_full = "drop_old"
keep_runs = 25
keep_for = "14d"

[storage]
max_size = "5gb"
min_free_space = "500mb"

[tasks.test-task]
description = "says hello"
cron = "*/5 * * * *"
on_overlap = "skip"
run = "echo hello"
`)

		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)

		task := cfg.Tasks[0]
		assert.Equal(t, "test-task", task.Name)
		assert.Equal(t, "*/5 * * * *", task.Cron)
		assert.True(t, task.APITrigger)
		assert.Equal(t, model.PolicySkip, task.OnOverlap)
		assert.Equal(t, 1, task.Parallelism)
		assert.Equal(t, 30*time.Minute, task.Timeout)
		assert.Equal(t, int64(200*1024*1024), task.LogMaxSize)
		assert.Equal(t, "drop_old", task.LogOnFull)
		assert.Equal(t, 25, task.KeepRuns)
		assert.Equal(t, 14*24*time.Hour, task.KeepFor)
		assert.Equal(t, int64(5*1024*1024*1024), cfg.Storage.MaxSize)
		assert.Equal(t, int64(500*1024*1024), cfg.Storage.MinFreeSpace)
		assert.True(t, cfg.Daemon.AllowCloudDispatch)
	})

	t.Run("api_trigger explicit false is preserved", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.silent]
api_trigger = false
cron = "*/5 * * * *"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		assert.False(t, cfg.Tasks[0].APITrigger)
	})

	t.Run("api_trigger defaults to true when absent", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.loud]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		assert.True(t, cfg.Tasks[0].APITrigger)
	})

	t.Run("unknown keys are rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
run = "echo hi"
bogus = 1
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "strict mode")
	})

	t.Run("malformed timeout is rejected at load", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
timeout = "garbage"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
	})

	t.Run("malformed log_max_size is rejected at load", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
log_max_size = "abc"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "log_max_size")
	})

	t.Run("service with defaults", func(t *testing.T) {
		path := writeTOML(t, `
[services.web]
run = "exec ./bin/web"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		s := cfg.Tasks[0]
		assert.Equal(t, model.KindService, s.Kind)
		assert.Equal(t, "Services", s.Group)
		assert.Equal(t, model.RestartAlways, s.Restart)
		assert.Equal(t, model.PolicySkip, s.OnOverlap)
		assert.Equal(t, 1, s.Instances)
		assert.Equal(t, time.Second, s.RestartDelay)
		assert.Equal(t, model.RestartBackoffExponential, s.RestartBackoff)
		assert.True(t, s.APITrigger)
	})

	t.Run("service with multiple instances", func(t *testing.T) {
		path := writeTOML(t, `
[services.worker]
instances = 5
run = "exec ./bin/worker"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		assert.Equal(t, 5, cfg.Tasks[0].Instances)
	})

	t.Run("service rejects cron", func(t *testing.T) {
		path := writeTOML(t, `
[services.web]
cron = "* * * * *"
run = "exec ./bin/web"
`)
		_, err := Load(path)
		require.Error(t, err)
	})

	t.Run("task rejects restart=always", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.web]
restart = "always"
run = "exec ./bin/web"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "[services.web]")
	})

	t.Run("task rejects instances", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.web]
instances = 3
run = "exec ./bin/web"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instances is only valid on [services.*]")
	})

	t.Run("name collision between tasks and services", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.foo]
run = "echo hi"

[services.foo]
run = "exec ./bin/foo"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "used by both")
	})

	t.Run("service with too many instances rejected", func(t *testing.T) {
		path := writeTOML(t, `
[services.web]
instances = 65
run = "exec ./bin/web"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instances")
	})
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "valid config",
			cfg:  &Config{Tasks: []model.Task{testTask("task1")}},
		},
		{
			name: "missing name",
			cfg: &Config{
				Tasks: []model.Task{{Run: "echo hello", Parallelism: 1, OnOverlap: model.PolicyQueue}},
			},
			wantErr: "task name is required",
		},
		{
			name: "missing run command",
			cfg: &Config{
				Tasks: []model.Task{{Name: "task1", Parallelism: 1, OnOverlap: model.PolicyQueue}},
			},
			wantErr: "task run command is required",
		},
		{
			name: "duplicate name",
			cfg: &Config{
				Tasks: []model.Task{testTask("task1"), testTask("task1")},
			},
			wantErr: "duplicate task name",
		},
		{
			name: "invalid on_overlap",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:        "task1",
					Run:         "echo hello",
					Parallelism: 1,
					OnOverlap:   "invalid",
				}},
			},
			wantErr: "invalid on_overlap",
		},
		{
			name: "invalid retry_backoff",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:         "task1",
					Run:          "echo hello",
					Parallelism:  1,
					OnOverlap:    model.PolicyQueue,
					RetryBackoff: "weird",
				}},
			},
			wantErr: "invalid retry_backoff",
		},
		{
			name: "invalid log_on_full",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:        "task1",
					Run:         "echo hello",
					Parallelism: 1,
					OnOverlap:   model.PolicyQueue,
					LogOnFull:   "tail",
				}},
			},
			wantErr: "invalid log_on_full",
		},
		{
			name: "invalid catch_up",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:        "task1",
					Run:         "echo hello",
					Parallelism: 1,
					OnOverlap:   model.PolicyQueue,
					CatchUp:     "none",
				}},
			},
			wantErr: "invalid catch_up",
		},
		{
			name: "invalid restart",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:        "task1",
					Run:         "echo hello",
					Parallelism: 1,
					OnOverlap:   model.PolicyQueue,
					Restart:     "on-failure",
				}},
			},
			wantErr: "invalid restart",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.cfg)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := &Config{
		Defaults: Defaults{
			Timeout:    45 * time.Minute,
			LogMaxSize: 200 * 1024 * 1024,
			LogOnFull:  "drop_old",
			KeepRuns:   25,
			KeepFor:    14 * 24 * time.Hour,
		},
		Tasks: []model.Task{
			{Name: "uses-defaults", Run: "echo 1"},
			{
				Name:        "overrides",
				Run:         "echo 2",
				Timeout:     10 * time.Minute,
				Parallelism: 5,
				OnOverlap:   model.PolicySkip,
				LogMaxSize:  50 * 1024 * 1024,
				LogOnFull:   "kill_task",
				KeepRuns:    5,
			},
		},
	}

	ApplyDefaults(cfg)

	defaulted := cfg.Tasks[0]
	assert.Equal(t, 1, defaulted.Parallelism)
	assert.Equal(t, model.PolicyQueue, defaulted.OnOverlap)
	assert.Equal(t, model.MissedRunLatest, defaulted.CatchUp)
	assert.Equal(t, 45*time.Minute, defaulted.Timeout)
	assert.Equal(t, int64(200*1024*1024), defaulted.LogMaxSize)
	assert.Equal(t, "drop_old", defaulted.LogOnFull)
	assert.Equal(t, 25, defaulted.KeepRuns)
	assert.Equal(t, 14*24*time.Hour, defaulted.KeepFor)

	overridden := cfg.Tasks[1]
	assert.Equal(t, 5, overridden.Parallelism)
	assert.Equal(t, model.PolicySkip, overridden.OnOverlap)
	assert.Equal(t, 10*time.Minute, overridden.Timeout)
	assert.Equal(t, int64(50*1024*1024), overridden.LogMaxSize)
	assert.Equal(t, "kill_task", overridden.LogOnFull)
	assert.Equal(t, 5, overridden.KeepRuns)
	assert.Equal(t, 14*24*time.Hour, overridden.KeepFor)
}

func TestApplyDefaultsBuiltinFallback(t *testing.T) {
	cfg := &Config{
		Tasks: []model.Task{{Name: "bare", Run: "echo hi"}},
	}

	ApplyDefaults(cfg)

	assert.Equal(t, int64(100*1024*1024), cfg.Tasks[0].LogMaxSize)
	assert.Equal(t, model.LogOverflowDropOld, cfg.Tasks[0].LogOnFull)
}
