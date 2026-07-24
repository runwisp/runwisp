// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildProcessEnv(t *testing.T) {
	t.Run("sorts output", func(t *testing.T) {
		got := buildProcessEnv([]string{"B=2", "A=1", "C=3"})
		require.Equal(t, []string{"A=1", "B=2", "C=3"}, got)
		assert.True(t, sort.StringsAreSorted(got))
	})

	t.Run("later layer overrides earlier", func(t *testing.T) {
		got := buildProcessEnv(
			[]string{"FOO=parent"},
			map[string]string{"FOO": "layer1", "BAR": "layer1"},
			map[string]string{"FOO": "layer2"},
		)
		assert.Equal(t, []string{"BAR=layer1", "FOO=layer2"}, got)
	})

	t.Run("parent preserved when no overlay sets the key", func(t *testing.T) {
		got := buildProcessEnv(
			[]string{"PATH=/usr/bin", "HOME=/root"},
			map[string]string{"EXTRA": "yes"},
		)
		assert.Equal(t, []string{"EXTRA=yes", "HOME=/root", "PATH=/usr/bin"}, got)
	})

	t.Run("drops parent entries without =", func(t *testing.T) {
		got := buildProcessEnv([]string{"VALID=ok", "INVALID_NO_EQ"})
		assert.Equal(t, []string{"VALID=ok"}, got)
	})

	t.Run("value with embedded = is preserved", func(t *testing.T) {
		got := buildProcessEnv(
			[]string{"URL=https://example.com/?a=b&c=d"},
		)
		assert.Equal(t, []string{"URL=https://example.com/?a=b&c=d"}, got)
	})

	t.Run("nil parent and nil layers", func(t *testing.T) {
		got := buildProcessEnv(nil)
		assert.Empty(t, got)
	})

	t.Run("strips RUNWISP_ daemon secrets from parent base", func(t *testing.T) {
		got := buildProcessEnv([]string{
			"PATH=/usr/bin",
			"RUNWISP_PASSWORD=hunter2",
			"RUNWISP_CLOUD_TOKEN=abc123",
			"HOME=/root",
		})
		assert.Equal(t, []string{"HOME=/root", "PATH=/usr/bin"}, got)
		for _, e := range got {
			assert.NotContains(t, e, "RUNWISP_", "daemon secret leaked into child env: %s", e)
		}
	})
}

// TestShellBackend_NoEnvKeepsInheritedEnv guards the regression where naively
// always setting cmd.Env would *replace* the daemon's environment for env-less
// tasks. Tasks without an env overlay must see the daemon's PATH/HOME/etc.
func TestShellBackend_NoEnvKeepsInheritedEnv(t *testing.T) {
	t.Setenv("RUNWISP_TEST_INHERIT", "yes")

	dir := t.TempDir()
	out := filepath.Join(dir, "env.out")

	task := &model.Task{Name: "inherit", GracefulStop: time.Second}
	script := `echo "$RUNWISP_TEST_INHERIT" > ` + out

	ctx := context.Background()
	backend := &ShellBackend{}
	proc, err := backend.Start(ctx, task, nil, &model.ShellExecution{Script: script})
	require.NoError(t, err)
	io.Copy(io.Discard, proc.Stdout)
	io.Copy(io.Discard, proc.Stderr)
	code, _ := proc.Wait()
	require.Equal(t, 0, code)

	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "yes", strings.TrimSpace(string(data)),
		"task with no env overlay must inherit the daemon's environment")
}

// TestShellBackend_TaskEnvOverlay verifies inline env and env_file values both
// reach the spawned process, with task-level values overriding the inherited
// daemon environment.
func TestShellBackend_TaskEnvOverlay(t *testing.T) {
	t.Setenv("RUNWISP_TEST_OVERRIDE", "from-parent")

	dir := t.TempDir()
	out := filepath.Join(dir, "env.out")

	task := &model.Task{
		Name:         "overlay",
		GracefulStop: time.Second,
		Env: map[string]string{
			"INLINE":                "from-task",
			"RUNWISP_TEST_OVERRIDE": "from-task",
		},
		Secrets: map[string]string{
			"SECRET": "from-file",
		},
	}
	script := `env | grep -E '^(INLINE|RUNWISP_TEST_OVERRIDE|SECRET)=' | sort > ` + out

	ctx := context.Background()
	backend := &ShellBackend{}
	proc, err := backend.Start(ctx, task, nil, &model.ShellExecution{Script: script})
	require.NoError(t, err)
	io.Copy(io.Discard, proc.Stdout)
	io.Copy(io.Discard, proc.Stderr)
	code, _ := proc.Wait()
	require.Equal(t, 0, code)

	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t,
		"INLINE=from-task\nRUNWISP_TEST_OVERRIDE=from-task\nSECRET=from-file\n",
		string(data),
	)
}

// TestContainerBuildContainerConfigOverlay verifies that task.Env and
// task.Secrets overlay on top of ContainerExecution.Env without invoking
// Docker. Direct unit test on buildContainerConfig.
func TestContainerBuildContainerConfigOverlay(t *testing.T) {
	b := &ContainerBackend{}
	ctr := &model.ContainerExecution{
		Env: []model.KeyValue{
			{Key: "CTR_ONLY", Value: "ctr"},
			{Key: "SHARED", Value: "from-ctr"},
		},
	}
	task := &model.Task{
		Name: "t",
		Env: map[string]string{
			"TASK_ONLY": "task",
			"SHARED":    "from-task",
		},
		Secrets: map[string]string{
			"SECRET": "from-file",
		},
	}
	cfg, _ := b.buildContainerConfig("image:tag", ctr, task, nil)
	assert.Contains(t, cfg.Env, "CTR_ONLY=ctr")
	assert.Contains(t, cfg.Env, "TASK_ONLY=task")
	assert.Contains(t, cfg.Env, "SECRET=from-file")
	assert.Contains(t, cfg.Env, "SHARED=from-task", "task.Env must override ContainerExecution.Env on key collision")
	assert.NotContains(t, cfg.Env, "SHARED=from-ctr")
}

func TestContainerBuildContainerConfigNoTaskEnv(t *testing.T) {
	b := &ContainerBackend{}
	ctr := &model.ContainerExecution{
		Env: []model.KeyValue{{Key: "A", Value: "1"}},
	}
	cfg, _ := b.buildContainerConfig("image:tag", ctr, &model.Task{Name: "t"}, nil)
	assert.Equal(t, []string{"A=1"}, cfg.Env)
}
