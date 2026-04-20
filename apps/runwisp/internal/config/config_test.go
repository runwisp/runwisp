// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTask(name string) model.Task {
	return model.Task{
		Name: name,
		Run:  "echo hello",
		Execution: model.TaskExecution{
			Concurrency: model.TaskConcurrency{
				Limit:  1,
				Policy: model.PolicyQueue,
			},
		},
	}
}

func TestLoad(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		content := `
daemon:
  cloudShellTasks: true

defaults:
  timeout: 30m
  logs:
    maxSize: 200mb
    overflow: head
  retention:
    runs: 25
    age: 14d

storage:
  maxSize: 5gb
  minFreeSpace: 500mb

tasks:
  test-task:
    description: "says hello"
    trigger:
      api: true
      cron: "*/5 * * * *"
    execution:
      concurrency:
        policy: skip
    run: echo hello
`
		tmpfile, err := os.CreateTemp("", "config-*.yaml")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write([]byte(content))
		require.NoError(t, err)
		require.NoError(t, tmpfile.Close())

		cfg, err := Load(tmpfile.Name())
		require.NoError(t, err)
		require.Len(t, cfg.Tasks, 1)

		task := cfg.Tasks[0]
		assert.Equal(t, "test-task", task.Name)
		assert.Equal(t, "*/5 * * * *", task.Trigger.Cron)
		assert.True(t, task.Trigger.APIEnabled())
		assert.Equal(t, model.PolicySkip, task.Execution.Concurrency.Policy)
		assert.Equal(t, 1, task.Execution.Concurrency.Limit)
		assert.Equal(t, "30m", task.Execution.Timeout)
		assert.Equal(t, "200mb", task.Logs.MaxSize)
		assert.Equal(t, "head", task.Logs.Overflow)
		assert.Equal(t, 25, task.Retention.Runs)
		assert.Equal(t, "14d", task.Retention.Age)
		assert.Equal(t, int64(5*1024*1024*1024), cfg.Storage.MaxSizeBytes)
		assert.Equal(t, int64(500*1024*1024), cfg.Storage.MinFreeSpaceBytes)
		assert.True(t, cfg.Daemon.CloudShellTasks)
	})

	t.Run("old schema is rejected", func(t *testing.T) {
		content := `
tasks:
  - name: old-task
    script: echo hello
`
		tmpfile, err := os.CreateTemp("", "config-*.yaml")
		require.NoError(t, err)
		defer os.Remove(tmpfile.Name())

		_, err = tmpfile.Write([]byte(content))
		require.NoError(t, err)
		require.NoError(t, tmpfile.Close())

		_, err = Load(tmpfile.Name())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tasks must be a mapping")
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
			cfg: &Config{
				Tasks: []model.Task{testTask("task1")},
			},
		},
		{
			name: "missing name",
			cfg: &Config{
				Tasks: []model.Task{{Run: "echo hello", Execution: model.TaskExecution{Concurrency: model.TaskConcurrency{Limit: 1, Policy: model.PolicyQueue}}}},
			},
			wantErr: "task name is required",
		},
		{
			name: "missing run command",
			cfg: &Config{
				Tasks: []model.Task{{Name: "task1", Execution: model.TaskExecution{Concurrency: model.TaskConcurrency{Limit: 1, Policy: model.PolicyQueue}}}},
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
			name: "invalid concurrency policy",
			cfg: &Config{
				Tasks: []model.Task{{
					Name: "task1",
					Run:  "echo hello",
					Execution: model.TaskExecution{
						Concurrency: model.TaskConcurrency{
							Limit:  1,
							Policy: "invalid",
						},
					},
				}},
			},
			wantErr: "invalid execution.concurrency.policy",
		},
		{
			name: "invalid retry backoff",
			cfg: &Config{
				Tasks: []model.Task{{
					Name: "task1",
					Run:  "echo hello",
					Execution: model.TaskExecution{
						Concurrency: model.TaskConcurrency{
							Limit:  1,
							Policy: model.PolicyQueue,
						},
					},
					Retry: model.TaskRetry{Backoff: "weird"},
				}},
			},
			wantErr: "invalid retry.backoff",
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
			Timeout: "45m",
			Logs: model.TaskLogs{
				MaxSize:  "200mb",
				Overflow: "head",
			},
			Retention: model.TaskRetention{
				Runs: 25,
				Age:  "14d",
			},
		},
		Tasks: []model.Task{
			{Name: "uses-defaults", Run: "echo 1"},
			{
				Name: "overrides",
				Run:  "echo 2",
				Execution: model.TaskExecution{
					Timeout: "10m",
					Concurrency: model.TaskConcurrency{
						Limit:  5,
						Policy: model.PolicySkip,
					},
				},
				Logs: model.TaskLogs{
					MaxSize:  "50mb",
					Overflow: "kill",
				},
				Retention: model.TaskRetention{
					Runs: 5,
				},
			},
		},
	}

	ApplyDefaults(cfg)

	defaulted := cfg.Tasks[0]
	assert.Equal(t, 1, defaulted.Execution.Concurrency.Limit)
	assert.Equal(t, model.PolicyQueue, defaulted.Execution.Concurrency.Policy)
	assert.Equal(t, model.MissedRunLatest, defaulted.Trigger.Catchup)
	assert.Equal(t, "45m", defaulted.Execution.Timeout)
	assert.Equal(t, "200mb", defaulted.Logs.MaxSize)
	assert.Equal(t, "head", defaulted.Logs.Overflow)
	assert.Equal(t, 25, defaulted.Retention.Runs)
	assert.Equal(t, "14d", defaulted.Retention.Age)
	assert.Equal(t, int64(200*1024*1024), defaulted.Logs.MaxSizeBytes)

	overridden := cfg.Tasks[1]
	assert.Equal(t, 5, overridden.Execution.Concurrency.Limit)
	assert.Equal(t, model.PolicySkip, overridden.Execution.Concurrency.Policy)
	assert.Equal(t, "10m", overridden.Execution.Timeout)
	assert.Equal(t, "50mb", overridden.Logs.MaxSize)
	assert.Equal(t, "kill", overridden.Logs.Overflow)
	assert.Equal(t, 5, overridden.Retention.Runs)
	assert.Equal(t, "14d", overridden.Retention.Age)
}

func TestApplyDefaultsBuiltinFallback(t *testing.T) {
	cfg := &Config{
		Tasks: []model.Task{{Name: "bare", Run: "echo hi"}},
	}

	ApplyDefaults(cfg)

	assert.Equal(t, "100mb", cfg.Tasks[0].Logs.MaxSize)
	assert.Equal(t, "tail", cfg.Tasks[0].Logs.Overflow)
	assert.Equal(t, int64(100*1024*1024), cfg.Tasks[0].Logs.MaxSizeBytes)
}
