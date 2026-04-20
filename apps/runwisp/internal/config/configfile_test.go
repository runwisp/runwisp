// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.yaml")

	doc, created, err := EnsureConfigFile(path)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotNil(t, doc)

	doc2, created2, err := EnsureConfigFile(path)
	require.NoError(t, err)
	assert.False(t, created2)
	assert.NotNil(t, doc2)
}

func TestAddTaskToDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.yaml")

	doc, _, err := EnsureConfigFile(path)
	require.NoError(t, err)

	task := model.Task{
		Description: "test task",
		Trigger:     model.TaskTrigger{Cron: "*/5 * * * *"},
		Run:         "echo hello",
	}
	require.NoError(t, AddTaskToDocument(doc, "my-task", task))
	require.NoError(t, WriteDocument(path, doc))

	names := TaskNamesFromDocument(doc)
	assert.Equal(t, []string{"my-task"}, names)

	cfg, err := LoadRaw(path)
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	assert.Equal(t, "my-task", cfg.Tasks[0].Name)
	assert.Equal(t, "*/5 * * * *", cfg.Tasks[0].Trigger.Cron)
	assert.Equal(t, "echo hello", cfg.Tasks[0].Run)
}

func TestAddTaskDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.yaml")

	doc, _, err := EnsureConfigFile(path)
	require.NoError(t, err)

	task := model.Task{Run: "echo hello"}
	require.NoError(t, AddTaskToDocument(doc, "my-task", task))
	assert.Error(t, AddTaskToDocument(doc, "my-task", task))
}

func TestUpdateTaskInDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.yaml")

	doc, _, err := EnsureConfigFile(path)
	require.NoError(t, err)

	task := model.Task{Run: "echo hello", Trigger: model.TaskTrigger{Cron: "* * * * *"}}
	require.NoError(t, AddTaskToDocument(doc, "my-task", task))

	updated := model.Task{Run: "echo updated", Trigger: model.TaskTrigger{Cron: "0 * * * *"}}
	require.NoError(t, UpdateTaskInDocument(doc, "my-task", "my-task", updated))
	require.NoError(t, WriteDocument(path, doc))

	cfg, err := LoadRaw(path)
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	assert.Equal(t, "echo updated", cfg.Tasks[0].Run)
	assert.Equal(t, "0 * * * *", cfg.Tasks[0].Trigger.Cron)
}

func TestUpdateTaskRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.yaml")

	doc, _, err := EnsureConfigFile(path)
	require.NoError(t, err)

	task := model.Task{Run: "echo hello"}
	require.NoError(t, AddTaskToDocument(doc, "old-name", task))

	require.NoError(t, UpdateTaskInDocument(doc, "old-name", "new-name", task))
	require.NoError(t, WriteDocument(path, doc))

	names := TaskNamesFromDocument(doc)
	assert.Equal(t, []string{"new-name"}, names)
}

func TestAPIDefaultTrue(t *testing.T) {
	cfg := &Config{
		Tasks: []model.Task{
			{Name: "no-api-set", Run: "echo hi", Execution: model.TaskExecution{Concurrency: model.TaskConcurrency{Limit: 1, Policy: model.PolicyQueue}}},
		},
	}

	ApplyDefaults(cfg)

	require.NotNil(t, cfg.Tasks[0].Trigger.API)
	assert.True(t, *cfg.Tasks[0].Trigger.API)
	assert.True(t, cfg.Tasks[0].Trigger.APIEnabled())
}

func TestAPIExplicitFalse(t *testing.T) {
	f := false
	cfg := &Config{
		Tasks: []model.Task{
			{
				Name: "no-api",
				Run:  "echo hi",
				Trigger: model.TaskTrigger{
					API: &f,
				},
				Execution: model.TaskExecution{Concurrency: model.TaskConcurrency{Limit: 1, Policy: model.PolicyQueue}},
			},
		},
	}

	ApplyDefaults(cfg)

	require.NotNil(t, cfg.Tasks[0].Trigger.API)
	assert.False(t, *cfg.Tasks[0].Trigger.API)
	assert.False(t, cfg.Tasks[0].Trigger.APIEnabled())
}
