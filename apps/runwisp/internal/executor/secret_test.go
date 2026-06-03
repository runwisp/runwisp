// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package executor

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainProc reads both pipes to completion and returns stdout. Reaping the
// process afterwards keeps the test from leaking a zombie.
func drainProc(t *testing.T, proc *Process) string {
	t.Helper()
	out, err := io.ReadAll(proc.Stdout)
	require.NoError(t, err)
	_, _ = io.ReadAll(proc.Stderr)
	_, _ = proc.Wait()
	return string(out)
}

// TestShellBackend_ResolvesScriptAndEnv proves the executor seam: a RunWisp
// ${VAR} in the script is resolved before /bin/sh sees it, an inline env value
// ${VAR} is resolved into the process env, and a bare $VAR the operator wrote
// is left for the shell to expand at runtime.
func TestShellBackend_ResolvesScriptAndEnv(t *testing.T) {
	t.Setenv("RW_EXEC_GREETING", "hello-from-var")
	t.Setenv("RW_EXEC_ENVVAL", "env-secret-123")

	backend := &ShellBackend{resolver: &SecretResolver{Redactor: redact.New()}}
	script := `echo "${RW_EXEC_GREETING}"; echo "$INJECTED"`
	task := &model.Task{
		Name: "resolve-test",
		Run:  script, // config origin: the script came from `run =` on disk
		Env:  map[string]string{"INJECTED": "${RW_EXEC_ENVVAL}"},
	}

	proc, err := backend.Start(context.Background(), task, &model.Run{ID: "r1"}, &model.ShellExecution{Script: script})
	require.NoError(t, err)

	got := drainProc(t, proc)
	assert.Contains(t, got, "hello-from-var", "RunWisp ${VAR} in the script resolves before /bin/sh")
	assert.Contains(t, got, "env-secret-123", "inline env ${VAR} resolves into the process env")
}

// TestShellBackend_MissingScriptVarFailsStart asserts an unresolvable ${VAR}
// with no :-default fails the start loudly rather than running with a literal
// placeholder — nothing silently fails.
func TestShellBackend_MissingScriptVarFailsStart(t *testing.T) {
	require.NoError(t, os.Unsetenv("RW_EXEC_DEFINITELY_MISSING"))

	backend := &ShellBackend{resolver: &SecretResolver{Redactor: redact.New()}}
	script := `echo "${RW_EXEC_DEFINITELY_MISSING}"`
	task := &model.Task{Name: "missing-var", Run: script} // config origin

	_, err := backend.Start(context.Background(), task, &model.Run{ID: "r1"}, &model.ShellExecution{Script: script})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RW_EXEC_DEFINITELY_MISSING")
}

// TestShellBackend_CloudScriptRunsVerbatim proves the origin gate: a script that
// did not come from `run =` on disk (empty task.Run, as a cloud-dispatched
// ad-hoc execution arrives) is handed to /bin/sh verbatim. RunWisp does not
// interpolate ${...} into it — an unset, defaultless ${VAR} that would fail a
// config script's resolution instead runs cleanly, since /bin/sh just expands
// it to empty. ${...} is a runwisp.toml feature; untrusted peer scripts are not
// rewritten by it.
func TestShellBackend_CloudScriptRunsVerbatim(t *testing.T) {
	require.NoError(t, os.Unsetenv("RW_CLOUD_DEFINITELY_MISSING"))

	backend := &ShellBackend{resolver: &SecretResolver{Redactor: redact.New()}}
	// No task.Run: this mirrors buildDynamicCloudTask, which sets ExecutionDef
	// directly and leaves Run empty.
	task := &model.Task{Name: "cloud-inline"}
	script := `echo "[${RW_CLOUD_DEFINITELY_MISSING}]"`

	proc, err := backend.Start(context.Background(), task, &model.Run{ID: "r1"}, &model.ShellExecution{Script: script})
	require.NoError(t, err, "cloud-origin script must run verbatim, not fail RunWisp resolution")

	got := drainProc(t, proc)
	assert.Contains(t, got, "[]", "/bin/sh expands the unset var to empty; RunWisp left it alone")
}

// TestSecretResolver_FileValueSeedsRedactor proves a ${file:...} resolved at
// spawn is registered with the redactor (file contents can change post-boot, so
// the executor re-seeds them), and that only the file's content — not the whole
// interpolated string — becomes the masked value.
func TestSecretResolver_FileValueSeedsRedactor(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tok.secret"), []byte("file-token-xyz\n"), 0o600))

	red := redact.New()
	resolver := &SecretResolver{DataDir: dir, Redactor: red}

	resolved, err := resolver.value("Bearer ${file:tok.secret}")
	require.NoError(t, err)
	assert.Equal(t, "Bearer file-token-xyz", resolved)

	// The redactor now masks the file content wherever it appears in output.
	assert.Equal(t, "got [redacted] here", red.Redact("got file-token-xyz here"))
}
