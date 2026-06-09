// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withCapturedStdout runs fn while stdout is redirected to a pipe; returns
// what was written. Used for commands that print straight to os.Stdout.
func withCapturedStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	var wg sync.WaitGroup
	var buf bytes.Buffer
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&buf, r)
	}()

	fn()

	require.NoError(t, w.Close())
	wg.Wait()
	os.Stdout = old
	return buf.String()
}

const minimalCfgEmpty = `# minimal runwisp.toml with no tasks
`

const minimalCfgOneTask = `
[tasks.hello]
cron = "* * * * *"
run = "echo hi"
description = "say hello"
`

const minimalCfgServiceTask = `
[services.worker]
instances = 3
run = "/usr/bin/worker"
api_trigger = true
`

const minimalCfgLongDescription = `
[tasks.longdesc]
cron = "0 * * * *"
run = "true"
description = "this is a very long description that should be truncated when rendered as a table row because it exceeds fifty characters"
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	orig := flags.CfgFile
	flags.CfgFile = path
	t.Cleanup(func() { flags.CfgFile = orig })
	return path
}

func TestRunList_NoTasks(t *testing.T) {
	writeConfig(t, minimalCfgEmpty)
	out := withCapturedStdout(t, func() {
		require.NoError(t, runList())
	})
	assert.Contains(t, out, "No tasks configured")
}

func TestRunList_OneCronTask(t *testing.T) {
	writeConfig(t, minimalCfgOneTask)
	out := withCapturedStdout(t, func() {
		require.NoError(t, runList())
	})
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "* * * * *")
	assert.Contains(t, out, "say hello")
}

func TestRunList_ServiceTaskShowsInstances(t *testing.T) {
	writeConfig(t, minimalCfgServiceTask)
	out := withCapturedStdout(t, func() {
		require.NoError(t, runList())
	})
	assert.Contains(t, out, "worker")
	assert.Contains(t, out, "(service x3)")
	assert.Contains(t, out, "yes", "api_trigger=true renders 'yes'")
}

func TestRunList_LongDescriptionTruncated(t *testing.T) {
	writeConfig(t, minimalCfgLongDescription)
	out := withCapturedStdout(t, func() {
		require.NoError(t, runList())
	})
	assert.Contains(t, out, "...")
}

func TestRunList_MissingConfigFile(t *testing.T) {
	orig := flags.CfgFile
	flags.CfgFile = filepath.Join(t.TempDir(), "absent.toml")
	t.Cleanup(func() { flags.CfgFile = orig })

	err := runList()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load")
}
