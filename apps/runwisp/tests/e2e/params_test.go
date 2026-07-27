//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParamsAppliedToProcessAndPersisted verifies the full per-execution
// parameter path against the real binary: declared parameters become env vars
// and argv tokens (defaults filled, flags rendered, supplied values winning),
// the values are passed inertly (never re-interpreted by the shell), and the
// resolved set is recorded on the run and returned by the API.
func TestParamsAppliedToProcessAndPersisted(t *testing.T) {
	const taskName = "param-task"

	dir := t.TempDir()
	out := filepath.Join(dir, "args.out")

	configPath := filepath.Join(dir, "runwisp.toml")
	// The run records its env var, then prints every appended argv token via a
	// trailing `printf` whose %s format cycles over the parameter tokens that
	// RunWisp splices onto the command. This mirrors the documented semantics
	// (tokens appended to the command, not exposed as "$@"). A malicious-looking
	// value proves shell-quoting keeps it a single inert token.
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[tasks.%s]
run = "echo \"PROJECT_ID=$PROJECT_ID\" > %s; printf 'arg=%%s\\n' >> %s"
params = [
  { env = "PROJECT_ID", required = true },
  { arg = "source", required = true },
  { arg = "dest", default = "/backups" },
  { option = "--region", choices = ["us", "eu"] },
  { flag = "--force" },
]
`, taskName, out, out)
	require.NoError(t, os.WriteFile(configPath, []byte(body), 0o600))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	projectID, source, region, force := "acme", "/tmp/some dir; rm -rf /", "eu", "true"
	triggered, err := client.TriggerRun(taskName, map[string]*string{
		"PROJECT_ID": &projectID,
		"source":     &source,
		"--region":   &region,
		"--force":    &force,
	})
	require.NoError(t, err)
	require.NotEmpty(t, triggered.ID)

	deadline := time.Now().Add(10 * time.Second)
	var captured []byte
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(out); err == nil && len(data) > 0 {
			captured = data
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotEmpty(t, captured, "param capture file never materialized")

	assert.Equal(t,
		"PROJECT_ID=acme\n"+
			"arg=/tmp/some dir; rm -rf /\n"+ // one inert argv token, shell never split or ran it
			"arg=/backups\n"+ // default applied
			"arg=--region\n"+
			"arg=eu\n"+
			"arg=--force\n",
		string(captured),
		"env + positional + option + flag tokens must reach the process verbatim, defaults filled",
	)

	// The resolved parameter set is persisted on the run and visible via the API.
	run, err := client.GetRun(taskName, triggered.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"PROJECT_ID": "acme",
		"source":     "/tmp/some dir; rm -rf /",
		"dest":       "/backups",
		"--region":   "eu",
		"--force":    "true",
	}, run.Params)
}

// TestParamsOmitVsEmptyString verifies the tri-state supplied map against the
// real binary: an explicit nil omits a defaulted option entirely (the default is
// not re-injected), while an explicit empty string is passed through as a real,
// empty argv value.
func TestParamsOmitVsEmptyString(t *testing.T) {
	const taskName = "note-task"

	dir := t.TempDir()
	out := filepath.Join(dir, "args.out")

	configPath := filepath.Join(dir, "runwisp.toml")
	// A single printf whose %%s format cycles over the appended option tokens, so
	// the captured file shows exactly which tokens RunWisp spliced on.
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[tasks.%s]
run = "printf 'arg=[%%s]\\n' > %s"
params = [
  { option = "--note", default = "hello" },
]
`, taskName, out)
	require.NoError(t, os.WriteFile(configPath, []byte(body), 0o600))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	readCapture := func(t *testing.T) string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if data, err := os.ReadFile(out); err == nil && len(data) > 0 {
				return string(data)
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("param capture file never materialized")
		return ""
	}

	// Explicit omit (nil): the default must NOT be re-injected, so no --note
	// token reaches the command at all.
	omit, err := client.TriggerRun(taskName, map[string]*string{"--note": nil})
	require.NoError(t, err)
	require.Equal(t, "arg=[]\n", readCapture(t), "omitted option leaves no argv tokens (printf sees no args)")
	omitRun, err := client.GetRun(taskName, omit.ID)
	require.NoError(t, err)
	assert.NotContains(t, omitRun.Params, "--note", "omitted param is absent from run history")

	require.NoError(t, os.Remove(out))

	// Explicit empty string: --note is passed with an empty value.
	empty := ""
	emptyRun, err := client.TriggerRun(taskName, map[string]*string{"--note": &empty})
	require.NoError(t, err)
	require.Equal(t, "arg=[--note]\narg=[]\n", readCapture(t),
		"empty string is passed as a real, empty option value")
	emptyDetail, err := client.GetRun(taskName, emptyRun.ID)
	require.NoError(t, err)
	assert.Equal(t, "", emptyDetail.Params["--note"], "empty string is recorded on the run")
}
