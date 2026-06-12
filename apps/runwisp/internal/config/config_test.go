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
		Name:          name,
		Run:           "echo hello",
		MaxConcurrent: 1,
		OnOverlap:     model.PolicyQueue,
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

[scheduler]
timezone = "UTC"

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
		assert.Equal(t, 1, task.MaxConcurrent)
		assert.Equal(t, 30*time.Minute, task.Timeout)
		assert.Equal(t, int64(200*1024*1024), task.LogMaxSize)
		assert.Equal(t, "drop_old", task.LogOnFull)
		assert.Equal(t, 25, task.KeepRuns)
		assert.Equal(t, 14*24*time.Hour, task.KeepFor)
		assert.Equal(t, int64(5*1024*1024*1024), cfg.Storage.MaxSize)
		assert.Equal(t, int64(500*1024*1024), cfg.Storage.MinFreeSpace)
		assert.True(t, cfg.Daemon.AllowCloudDispatch)
		assert.True(t, cfg.IsCloudShellEnabled())
		assert.Equal(t, "UTC", cfg.Scheduler.Timezone)
		assert.Equal(t, TimezoneSourceConfig, cfg.Scheduler.Source)
	})

	t.Run("colon in quoted task name", func(t *testing.T) {
		path := writeTOML(t, `
[tasks."db:backup"]
cron = "*/5 * * * *"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		assert.Equal(t, "db:backup", cfg.Tasks[0].Name)
	})

	t.Run("api_trigger explicit false is preserved", func(t *testing.T) {
		path := writeTOML(t, `
[scheduler]
timezone = "UTC"

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
		assert.Contains(t, err.Error(), "unknown key")
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
		assert.Equal(t, model.BackoffExponential, s.RestartBackoff)
		assert.Equal(t, DefaultHealthyAfter, s.HealthyAfter)
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

	t.Run("service rejects max_concurrent (not a service knob)", func(t *testing.T) {
		path := writeTOML(t, `
[services.web]
max_concurrent = 3
run = "exec ./bin/web"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown key")
	})

	t.Run("service rejects queue_max (not a service knob)", func(t *testing.T) {
		path := writeTOML(t, `
[services.web]
queue_max = 10
run = "exec ./bin/web"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown key")
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
				Tasks: []model.Task{{Run: "echo hello", MaxConcurrent: 1, OnOverlap: model.PolicyQueue}},
			},
			wantErr: "task name is required",
		},
		{
			name: "missing run command",
			cfg: &Config{
				Tasks: []model.Task{{Name: "task1", MaxConcurrent: 1, OnOverlap: model.PolicyQueue}},
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
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: 1,
					OnOverlap:     "invalid",
				}},
			},
			wantErr: "invalid on_overlap",
		},
		{
			name: "invalid retry_backoff",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					RetryBackoff:  "weird",
				}},
			},
			wantErr: "invalid retry_backoff",
		},
		{
			name: "invalid log_on_full",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					LogOnFull:     "tail",
				}},
			},
			wantErr: "invalid log_on_full",
		},
		{
			name: "invalid catch_up",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					CatchUp:       "none",
				}},
			},
			wantErr: "invalid catch_up",
		},
		{
			name: "invalid restart",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					Restart:       "on-failure",
				}},
			},
			wantErr: "invalid restart",
		},
		{
			name: "max_concurrent above cap",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: MaxConcurrentCap + 1,
					OnOverlap:     model.PolicyQueue,
				}},
			},
			wantErr: "max_concurrent",
		},
		{
			name: "queue_max above cap",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: 1,
					QueueMax:      QueueMaxCap + 1,
					OnOverlap:     model.PolicyQueue,
				}},
			},
			wantErr: "queue_max",
		},
		{
			name: "retry_attempts above cap",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					RetryAttempts: RetryAttemptsCap + 1,
				}},
			},
			wantErr: "retry_attempts",
		},
		{
			name: "negative graceful_stop",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "task1",
					Run:           "echo hello",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					GracefulStop:  -time.Second,
				}},
			},
			wantErr: "graceful_stop",
		},
		{
			name: "negative shutdown_timeout",
			cfg: &Config{
				Daemon: Daemon{ShutdownTimeout: -time.Second},
				Tasks:  []model.Task{testTask("ok")},
			},
			wantErr: "daemon.shutdown_timeout",
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
				Name:          "overrides",
				Run:           "echo 2",
				Timeout:       10 * time.Minute,
				MaxConcurrent: 5,
				OnOverlap:     model.PolicySkip,
				LogMaxSize:    50 * 1024 * 1024,
				LogOnFull:     "kill_task",
				KeepRuns:      5,
			},
		},
	}

	ApplyDefaults(cfg)

	defaulted := cfg.Tasks[0]
	assert.Equal(t, 1, defaulted.MaxConcurrent)
	assert.Equal(t, model.PolicyQueue, defaulted.OnOverlap)
	assert.Equal(t, model.MissedRunLatest, defaulted.CatchUp)
	assert.Equal(t, 45*time.Minute, defaulted.Timeout)
	assert.Equal(t, int64(200*1024*1024), defaulted.LogMaxSize)
	assert.Equal(t, "drop_old", defaulted.LogOnFull)
	assert.Equal(t, 25, defaulted.KeepRuns)
	assert.Equal(t, 14*24*time.Hour, defaulted.KeepFor)
	assert.Equal(t, DefaultGracefulStop, defaulted.GracefulStop)
	assert.Equal(t, DefaultMaxCatchUpRuns, defaulted.MaxCatchUpRuns)
	assert.Equal(t, DefaultQueueMax, defaulted.QueueMax)

	overridden := cfg.Tasks[1]
	assert.Equal(t, 5, overridden.MaxConcurrent)
	assert.Equal(t, model.PolicySkip, overridden.OnOverlap)
	assert.Equal(t, 10*time.Minute, overridden.Timeout)
	assert.Equal(t, int64(50*1024*1024), overridden.LogMaxSize)
	assert.Equal(t, "kill_task", overridden.LogOnFull)
	assert.Equal(t, 5, overridden.KeepRuns)
	assert.Equal(t, 14*24*time.Hour, overridden.KeepFor)

	// Daemon shutdown_timeout fills in from the built-in default when
	// the operator omits it.
	assert.Equal(t, DefaultDaemonShutdown, cfg.Daemon.ShutdownTimeout)
	// [defaults] healthy_after picks up the built-in default too.
	assert.Equal(t, DefaultHealthyAfter, cfg.Defaults.HealthyAfter)
}

func TestNotifyOnMissedRules(t *testing.T) {
	t.Run("omitted defaults to notifying (true)", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.NotNil(t, cfg.Tasks[0].NotifyOnMissed)
		assert.True(t, *cfg.Tasks[0].NotifyOnMissed)
		assert.True(t, cfg.Tasks[0].NotifiesOnMissed())
	})

	t.Run("explicit false on a task mutes that task", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
run = "echo hi"
notify_on_missed = false
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.NotNil(t, cfg.Tasks[0].NotifyOnMissed)
		assert.False(t, *cfg.Tasks[0].NotifyOnMissed)
		assert.False(t, cfg.Tasks[0].NotifiesOnMissed())
	})

	t.Run("omitted inherits notify_on_missed = false from defaults", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
notify_on_missed = false

[tasks.inheritor]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.NotNil(t, cfg.Tasks[0].NotifyOnMissed)
		assert.False(t, *cfg.Tasks[0].NotifyOnMissed,
			"a task that omits the key inherits the muted default")
	})

	t.Run("explicit true overrides a muted default", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
notify_on_missed = false

[tasks.loud]
run = "echo hi"
notify_on_missed = true
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.NotNil(t, cfg.Tasks[0].NotifyOnMissed)
		assert.True(t, *cfg.Tasks[0].NotifyOnMissed,
			"a per-task true wins over a muted [defaults]")
	})
}

func TestJitterRules(t *testing.T) {
	t.Run("explicit jitter parses on a task", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
cron = "0 3 * * *"
jitter = "30m"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 30*time.Minute, cfg.Tasks[0].Jitter)
	})

	t.Run("omitted jitter inherits from defaults", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
jitter = "5m"

[tasks.inheritor]
cron = "0 3 * * *"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 5*time.Minute, cfg.Tasks[0].Jitter,
			"a cron task that omits jitter inherits [defaults] jitter")
	})

	t.Run("explicit task jitter overrides defaults", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
jitter = "5m"

[tasks.loud]
cron = "0 3 * * *"
jitter = "1m"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, time.Minute, cfg.Tasks[0].Jitter)
	})

	t.Run("services never inherit defaults jitter", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
jitter = "5m"

[services.svc]
run = "exec ./bin/svc"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)
		assert.Equal(t, time.Duration(0), cfg.Tasks[0].Jitter,
			"a service must not pick up [defaults] jitter — it has no fire time to spread")
	})

	t.Run("explicit jitter on a service is rejected as unknown", func(t *testing.T) {
		path := writeTOML(t, `
[services.svc]
run = "exec ./bin/svc"
jitter = "5m"
`)
		_, err := Load(path)
		require.Error(t, err, "jitter is not a [services.*] key")
	})

	t.Run("negative jitter is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
cron = "0 3 * * *"
jitter = "-5m"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "jitter")
	})

	t.Run("above-cap jitter is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
cron = "0 3 * * *"
jitter = "25h"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "jitter")
	})

	t.Run("above-cap defaults jitter is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
jitter = "25h"

[tasks.t]
cron = "0 3 * * *"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "jitter")
	})

	t.Run("zero jitter on a cron task is accepted", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
cron = "0 3 * * *"
jitter = "0s"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, time.Duration(0), cfg.Tasks[0].Jitter,
			"explicit zero jitter is the no-spread case, not an error")
	})

	t.Run("jitter on a cron-less task is tolerated", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
jitter = "5m"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 5*time.Minute, cfg.Tasks[0].Jitter,
			"jitter on a cron-less task is a harmless no-op, like catch_up")
	})
}

func TestKeepRunsRules(t *testing.T) {
	t.Run("positive integer is preserved", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_runs = 25
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 25, cfg.Tasks[0].KeepRuns)
	})

	t.Run("omitted keep_runs inherits from defaults", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
keep_runs = 50

[tasks.inheritor]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 50, cfg.Tasks[0].KeepRuns)
	})

	t.Run("negative integer is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_runs = -1
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keep_runs")
	})

	t.Run("above-cap value is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_runs = 1000001
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keep_runs")
	})
}

func TestKeepForRules(t *testing.T) {
	t.Run("positive duration is preserved", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_for = "30d"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 30*24*time.Hour, cfg.Tasks[0].KeepFor)
	})

	t.Run("omitted keep_for inherits from defaults", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
keep_for = "30d"

[tasks.inheritor]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 30*24*time.Hour, cfg.Tasks[0].KeepFor)
	})

	t.Run("zero is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_for = "0s"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keep_for")
	})

	t.Run("negative duration is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_for = "-30d"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keep_for")
	})

	// KeepForCap is ~100 years (36500d). A value exactly at the cap loads; just
	// over it is rejected as a typo guard. Covers both a per-task keep_for and
	// the [defaults] keep_for, which share validateKeepFor.
	t.Run("at cap is accepted", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_for = "36500d"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, KeepForCap, cfg.Tasks[0].KeepFor)
	})

	t.Run("above cap is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_for = "36501d"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keep_for")
		assert.Contains(t, err.Error(), "exceeds the cap")
	})

	// A value so large it overflows int64 nanoseconds is rejected even before
	// the cap check, by the duration parser itself. Either way it never loads.
	t.Run("overflowing value is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
keep_for = "999999d"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keep_for")
	})

	t.Run("above cap in defaults is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
keep_for = "36501d"

[tasks.t]
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "keep_for")
		assert.Contains(t, err.Error(), "exceeds the cap")
	})
}

// graceful_stop is validated with a `< 0` check, so an explicit "0s" is not an
// error. Zero is also the omitted-sentinel, so ApplyDefaults fills it with
// DefaultGracefulStop rather than leaving a zero grace period. Locking that.
func TestGracefulStopZeroAccepted(t *testing.T) {
	path := writeTOML(t, `
[tasks.t]
graceful_stop = "0s"
run = "echo hi"
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, DefaultGracefulStop, cfg.Tasks[0].GracefulStop)
}

func TestLogMaxSizeRules(t *testing.T) {
	t.Run("zero bytes are rejected", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
log_max_size = "0"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "log_max_size")
	})

	t.Run("positive byte size is preserved", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
log_max_size = "200mb"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, int64(200*1024*1024), cfg.Tasks[0].LogMaxSize)
	})
}

func TestSchedulerTimezoneFallback(t *testing.T) {
	t.Run("explicit timezone flagged as config-sourced", func(t *testing.T) {
		path := writeTOML(t, `
[scheduler]
timezone = "UTC"

[tasks.nightly]
cron = "0 2 * * *"
run  = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, "UTC", cfg.Scheduler.Timezone)
		assert.Equal(t, TimezoneSourceConfig, cfg.Scheduler.Source)
	})

	t.Run("missing timezone falls back to system zone, not an error", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.nightly]
cron = "0 2 * * *"
run  = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.NotEmpty(t, cfg.Scheduler.Timezone)
		assert.Equal(t, TimezoneSourceSystem, cfg.Scheduler.Source)
	})

	t.Run("invalid timezone is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[scheduler]
timezone = "Not/A/Zone"

[tasks.t]
cron = "* * * * *"
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timezone")
	})

	t.Run("per-task timezone overrides scheduler default", func(t *testing.T) {
		path := writeTOML(t, `
[scheduler]
timezone = "UTC"

[tasks.nightly]
cron     = "0 2 * * *"
timezone = "America/New_York"
run      = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, "America/New_York", cfg.Tasks[0].Timezone)
	})
}

func TestMaxCatchUpRunsRules(t *testing.T) {
	t.Run("omitted defaults to DefaultMaxCatchUpRuns", func(t *testing.T) {
		path := writeTOML(t, `
[scheduler]
timezone = "UTC"

[tasks.t]
cron = "* * * * *"
catch_up = "all"
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, DefaultMaxCatchUpRuns, cfg.Tasks[0].MaxCatchUpRuns)
	})

	t.Run("positive value is preserved", func(t *testing.T) {
		path := writeTOML(t, `
[scheduler]
timezone = "UTC"

[tasks.t]
cron              = "* * * * *"
max_catch_up_runs = 50
run               = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 50, cfg.Tasks[0].MaxCatchUpRuns)
	})

	t.Run("negative is rejected", func(t *testing.T) {
		path := writeTOML(t, `
[scheduler]
timezone = "UTC"

[tasks.t]
cron              = "* * * * *"
max_catch_up_runs = -1
run               = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_catch_up_runs")
	})

	t.Run("large value is accepted", func(t *testing.T) {
		path := writeTOML(t, `
[scheduler]
timezone = "UTC"

[tasks.t]
cron              = "* * * * *"
max_catch_up_runs = 100000
run               = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 100000, cfg.Tasks[0].MaxCatchUpRuns)
	})
}

func TestApplyDefaultsBuiltinFallback(t *testing.T) {
	cfg := &Config{
		Tasks: []model.Task{{Name: "bare", Run: "echo hi"}},
	}

	ApplyDefaults(cfg)

	assert.Equal(t, int64(100*1024*1024), cfg.Tasks[0].LogMaxSize)
	assert.Equal(t, model.LogOverflowDropOld, cfg.Tasks[0].LogOnFull)
}

func TestValidate_MoreCases(t *testing.T) {
	serviceTask := func(name string) model.Task {
		return model.Task{
			Name:          name,
			Kind:          model.KindService,
			Run:           "echo hello",
			Instances:     1,
			MaxConcurrent: 1,
			OnOverlap:     model.PolicyQueue,
		}
	}

	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "invalid defaults.log_on_full",
			cfg: &Config{
				Defaults: Defaults{LogOnFull: "bad"},
				Tasks:    []model.Task{testTask("t1")},
			},
			wantErr: "defaults.log_on_full",
		},
		{
			name: "negative defaults.keep_runs",
			cfg: &Config{
				Defaults: Defaults{KeepRuns: -1},
				Tasks:    []model.Task{testTask("t1")},
			},
			wantErr: "defaults.keep_runs",
		},
		{
			name: "negative defaults.keep_for",
			cfg: &Config{
				Defaults: Defaults{KeepFor: -time.Second},
				Tasks:    []model.Task{testTask("t1")},
			},
			wantErr: "defaults.keep_for",
		},
		{
			name: "negative defaults.healthy_after",
			cfg: &Config{
				Defaults: Defaults{HealthyAfter: -time.Second},
				Tasks:    []model.Task{testTask("t1")},
			},
			wantErr: "defaults.healthy_after",
		},
		{
			name: "whitespace-only run script",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "t1",
					Run:           "   ",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
				}},
			},
			wantErr: "task run command is required",
		},
		{
			name: "negative max_concurrent",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "t1",
					Run:           "echo hi",
					MaxConcurrent: -1,
					OnOverlap:     model.PolicyQueue,
				}},
			},
			wantErr: "max_concurrent",
		},
		{
			name: "negative queue_max",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "t1",
					Run:           "echo hi",
					MaxConcurrent: 1,
					QueueMax:      -1,
					OnOverlap:     model.PolicyQueue,
				}},
			},
			wantErr: "queue_max",
		},
		{
			name: "negative retry_attempts",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "t1",
					Run:           "echo hi",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					RetryAttempts: -1,
				}},
			},
			wantErr: "retry_attempts",
		},
		{
			name: "negative keep_runs on task",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "t1",
					Run:           "echo hi",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					KeepRuns:      -1,
				}},
			},
			wantErr: "keep_runs",
		},
		{
			name: "negative keep_for on task",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "t1",
					Run:           "echo hi",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					KeepFor:       -time.Second,
				}},
			},
			wantErr: "keep_for",
		},
		{
			name: "invalid timezone on task",
			cfg: &Config{
				Tasks: []model.Task{{
					Name:          "t1",
					Run:           "echo hi",
					MaxConcurrent: 1,
					OnOverlap:     model.PolicyQueue,
					Timezone:      "Not/ATimezone",
				}},
			},
			wantErr: "timezone",
		},
		{
			name: "service instances == 0",
			cfg: &Config{
				Tasks: []model.Task{func() model.Task {
					t := serviceTask("svc1")
					t.Instances = 0
					return t
				}()},
			},
			wantErr: "instances",
		},
		{
			name: "service invalid restart_backoff",
			cfg: &Config{
				Tasks: []model.Task{func() model.Task {
					t := serviceTask("svc1")
					t.RestartBackoff = "weird"
					return t
				}()},
			},
			wantErr: "restart_backoff",
		},
		{
			name: "service negative healthy_after",
			cfg: &Config{
				Tasks: []model.Task{func() model.Task {
					t := serviceTask("svc1")
					t.HealthyAfter = -time.Second
					return t
				}()},
			},
			wantErr: "healthy_after",
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

func TestGracefulStopWarnings(t *testing.T) {
	t.Run("returns nil when shutdown_timeout is zero", func(t *testing.T) {
		cfg := &Config{
			Daemon: Daemon{ShutdownTimeout: 0},
			Tasks:  []model.Task{testTask("t1")},
		}
		cfg.Tasks[0].GracefulStop = 30 * time.Second
		warnings := gracefulStopWarnings(cfg)
		assert.Nil(t, warnings)
	})

	t.Run("returns nil when shutdown_timeout is negative", func(t *testing.T) {
		cfg := &Config{
			Daemon: Daemon{ShutdownTimeout: -time.Second},
			Tasks:  []model.Task{testTask("t1")},
		}
		cfg.Tasks[0].GracefulStop = 30 * time.Second
		warnings := gracefulStopWarnings(cfg)
		assert.Nil(t, warnings)
	})

	t.Run("returns warning when task graceful_stop exceeds shutdown_timeout", func(t *testing.T) {
		cfg := &Config{
			Daemon: Daemon{ShutdownTimeout: 10 * time.Second},
			Tasks:  []model.Task{testTask("t1")},
		}
		cfg.Tasks[0].GracefulStop = 30 * time.Second
		warnings := gracefulStopWarnings(cfg)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "t1")
		assert.Contains(t, warnings[0], "graceful_stop")
		assert.Contains(t, warnings[0], "shutdown_timeout")
	})

	t.Run("returns no warning when task graceful_stop is within shutdown_timeout", func(t *testing.T) {
		cfg := &Config{
			Daemon: Daemon{ShutdownTimeout: 30 * time.Second},
			Tasks:  []model.Task{testTask("t1")},
		}
		cfg.Tasks[0].GracefulStop = 10 * time.Second
		warnings := gracefulStopWarnings(cfg)
		assert.Empty(t, warnings)
	})
}

func TestLoad_ParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name:    "task timeout bad",
			toml:    "[tasks.t]\nrun = \"echo hi\"\ntimeout = \"bad\"\n",
			wantErr: "timeout",
		},
		{
			name:    "task graceful_stop bad",
			toml:    "[tasks.t]\nrun = \"echo hi\"\ngraceful_stop = \"bad\"\n",
			wantErr: "graceful_stop",
		},
		{
			name:    "task jitter bad",
			toml:    "[tasks.t]\nrun = \"echo hi\"\njitter = \"bad\"\n",
			wantErr: "jitter",
		},
		{
			name:    "defaults jitter bad",
			toml:    "[defaults]\njitter = \"bad\"\n\n[tasks.t]\nrun = \"echo hi\"\n",
			wantErr: "jitter",
		},
		{
			name:    "service timeout bad",
			toml:    "[services.svc]\nrun = \"exec ./bin/svc\"\ntimeout = \"bad\"\n",
			wantErr: "timeout",
		},
		{
			name:    "service graceful_stop bad",
			toml:    "[services.svc]\nrun = \"exec ./bin/svc\"\ngraceful_stop = \"bad\"\n",
			wantErr: "graceful_stop",
		},
		{
			name:    "service restart_delay bad",
			toml:    "[services.svc]\nrun = \"exec ./bin/svc\"\nrestart_delay = \"bad\"\n",
			wantErr: "restart_delay",
		},
		{
			name:    "service healthy_after bad",
			toml:    "[services.svc]\nrun = \"exec ./bin/svc\"\nhealthy_after = \"bad\"\n",
			wantErr: "healthy_after",
		},
		{
			name:    "service log_max_size bad",
			toml:    "[services.svc]\nrun = \"exec ./bin/svc\"\nlog_max_size = \"badsize\"\n",
			wantErr: "log_max_size",
		},
		{
			name:    "defaults timeout bad",
			toml:    "[defaults]\ntimeout = \"bad\"\n\n[tasks.t]\nrun = \"echo hi\"\n",
			wantErr: "timeout",
		},
		{
			name:    "defaults log_max_size bad",
			toml:    "[defaults]\nlog_max_size = \"badsize\"\n\n[tasks.t]\nrun = \"echo hi\"\n",
			wantErr: "log_max_size",
		},
		{
			name:    "defaults healthy_after bad",
			toml:    "[defaults]\nhealthy_after = \"bad\"\n\n[tasks.t]\nrun = \"echo hi\"\n",
			wantErr: "healthy_after",
		},
		{
			name:    "daemon shutdown_timeout bad",
			toml:    "[daemon]\nshutdown_timeout = \"bad\"\n\n[tasks.t]\nrun = \"echo hi\"\n",
			wantErr: "shutdown_timeout",
		},
		{
			name:    "storage max_size bad",
			toml:    "[storage]\nmax_size = \"badsize\"\n\n[tasks.t]\nrun = \"echo hi\"\n",
			wantErr: "max_size",
		},
		{
			name:    "storage min_free_space bad",
			toml:    "[storage]\nmin_free_space = \"badsize\"\n\n[tasks.t]\nrun = \"echo hi\"\n",
			wantErr: "min_free_space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// These are pure decode-layer failures (bad scalar formats caught
			// while converting wire types), so exercise decode directly rather
			// than writing a temp file and running the whole Load pipeline.
			_, err := decode([]byte(tt.toml), "/")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoad_ServiceAPITriggerFalse(t *testing.T) {
	path := writeTOML(t, `
[services.svc]
run = "exec ./bin/svc"
api_trigger = false
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	assert.False(t, cfg.Tasks[0].APITrigger)
}

func TestNewSchemaFields(t *testing.T) {
	t.Run("graceful_stop, max_concurrent, queue_max parse on tasks", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
graceful_stop  = "8s"
max_concurrent = 4
queue_max      = 50
run            = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		task := cfg.Tasks[0]
		assert.Equal(t, 8*time.Second, task.GracefulStop)
		assert.Equal(t, 4, task.MaxConcurrent)
		assert.Equal(t, 50, task.QueueMax)
	})

	t.Run("healthy_after parses on services and inherits from defaults", func(t *testing.T) {
		path := writeTOML(t, `
[defaults]
healthy_after = "2m"

[services.svc-with-default]
run = "exec ./bin/svc"

[services.svc-with-override]
healthy_after = "30s"
run = "exec ./bin/svc"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 2)
		// Tasks are sorted alphabetically.
		assert.Equal(t, 2*time.Minute, cfg.Tasks[0].HealthyAfter)
		assert.Equal(t, 30*time.Second, cfg.Tasks[1].HealthyAfter)
	})

	t.Run("daemon shutdown_timeout parses", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
shutdown_timeout = "20s"

[tasks.t]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, 20*time.Second, cfg.Daemon.ShutdownTimeout)
	})

	t.Run("daemon external_url parses and strips trailing slash", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
external_url = "https://runwisp.example.com/"

[tasks.t]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, "https://runwisp.example.com", cfg.Daemon.ExternalURL)
	})

	t.Run("daemon external_url rejects non-http schemes", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
external_url = "ftp://nope"

[tasks.t]
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "daemon.external_url")
	})

	t.Run("daemon external_url omitted is empty", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, "", cfg.Daemon.ExternalURL)
	})

	t.Run("daemon metrics_enabled defaults to false", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.False(t, cfg.Daemon.MetricsEnabled)
		assert.Equal(t, "", cfg.Daemon.MetricsListen)
	})

	t.Run("daemon metrics_enabled true with shared listener", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
metrics_enabled = true

[tasks.t]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.True(t, cfg.Daemon.MetricsEnabled)
		assert.Equal(t, "", cfg.Daemon.MetricsListen)
	})

	t.Run("daemon metrics_listen requires metrics_enabled", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
metrics_listen = "127.0.0.1:9478"

[tasks.t]
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "daemon.metrics_listen")
		assert.Contains(t, err.Error(), "metrics_enabled = true")
	})

	t.Run("daemon metrics_listen parses host:port", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
metrics_enabled = true
metrics_listen = "127.0.0.1:9478"

[tasks.t]
run = "echo hi"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.True(t, cfg.Daemon.MetricsEnabled)
		assert.Equal(t, "127.0.0.1:9478", cfg.Daemon.MetricsListen)
	})

	t.Run("daemon metrics_listen rejects non-numeric port", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
metrics_enabled = true
metrics_listen = "127.0.0.1:abc"

[tasks.t]
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "daemon.metrics_listen")
	})

	t.Run("daemon metrics_listen rejects malformed address", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
metrics_enabled = true
metrics_listen = "no-colon-here"

[tasks.t]
run = "echo hi"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "daemon.metrics_listen")
	})

	t.Run("type mismatch on max_concurrent triggers DecodeError path", func(t *testing.T) {
		// Passing a string where an integer is expected triggers a toml.DecodeError.
		path := writeTOML(t, `
[tasks.t]
run = "echo hi"
max_concurrent = "not-a-number"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse config file")
	})
}
