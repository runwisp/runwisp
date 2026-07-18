// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	return path
}

func runListString(t *testing.T, f Flags) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, runList(&buf, f, false))
	return buf.String()
}

func TestRunList_NoTasks(t *testing.T) {
	f := Flags{CfgFile: writeConfig(t, minimalCfgEmpty)}
	assert.Contains(t, runListString(t, f), "No tasks configured")
}

func TestRunList_OneCronTask(t *testing.T) {
	f := Flags{CfgFile: writeConfig(t, minimalCfgOneTask)}
	out := runListString(t, f)
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "* * * * *")
	assert.Contains(t, out, "say hello")
}

func TestRunList_ServiceTaskShowsInstances(t *testing.T) {
	f := Flags{CfgFile: writeConfig(t, minimalCfgServiceTask)}
	out := runListString(t, f)
	assert.Contains(t, out, "worker")
	assert.Contains(t, out, "(service x3)")
	assert.Contains(t, out, "yes", "api_trigger=true renders 'yes'")
}

func TestRunList_LongDescriptionTruncated(t *testing.T) {
	f := Flags{CfgFile: writeConfig(t, minimalCfgLongDescription)}
	assert.Contains(t, runListString(t, f), "...")
}

func TestRunList_MissingConfigFile(t *testing.T) {
	t.Parallel()
	f := Flags{CfgFile: filepath.Join(t.TempDir(), "absent.toml")}

	var buf bytes.Buffer
	err := runList(&buf, f, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load")
}

func TestRunList_JSONServiceAndCron(t *testing.T) {
	body := minimalCfgOneTask + minimalCfgServiceTask
	f := Flags{CfgFile: writeConfig(t, body)}

	var buf bytes.Buffer
	require.NoError(t, runList(&buf, f, true))

	var doc listJSONDoc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc))
	assert.Equal(t, jsonSchemaVersion, doc.SchemaVersion)
	require.Len(t, doc.Tasks, 2)

	byName := map[string]listTaskJSON{}
	for _, tk := range doc.Tasks {
		byName[tk.Name] = tk
	}
	assert.Equal(t, "task", byName["hello"].Kind)
	assert.Equal(t, "* * * * *", byName["hello"].Schedule)
	assert.Equal(t, "service", byName["worker"].Kind)
	assert.Equal(t, 3, byName["worker"].Instances)
	assert.True(t, byName["worker"].APITrigger)

	// max_concurrent is a real setting for tasks but not services — services'
	// copy count is `instances`. It must be present for the task and omitted
	// (nil) for the service, never fabricated as a stray `1`.
	require.NotNil(t, byName["hello"].MaxConcurrent)
	assert.Nil(t, byName["worker"].MaxConcurrent)
}

func TestRunList_JSONMissingConfigEmitsErrorDoc(t *testing.T) {
	f := Flags{CfgFile: filepath.Join(t.TempDir(), "absent.toml")}

	var buf bytes.Buffer
	err := runList(&buf, f, true)
	require.Error(t, err)

	var doc listJSONDoc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc), "stdout must still be valid JSON on failure")
	assert.Equal(t, jsonSchemaVersion, doc.SchemaVersion)
	assert.NotEmpty(t, doc.Error)
}
