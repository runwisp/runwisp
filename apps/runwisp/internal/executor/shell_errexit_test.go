// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runScript executes def to completion and returns stdout and the exit code.
// drainStdout discards the exit code, and every test here is about the exit
// code, so this variant exists alongside it rather than replacing it.
func runScript(t *testing.T, def *model.ShellExecution) (string, int) {
	t.Helper()
	backend := &ShellBackend{}
	proc, err := backend.Start(context.Background(), &model.Task{Name: "t"}, nil, def)
	require.NoError(t, err)

	var out bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&out, proc.Stdout) }()
	go func() { defer wg.Done(); _, _ = io.Copy(io.Discard, proc.Stderr) }()
	wg.Wait()
	exit, _ := proc.Wait()
	return out.String(), exit
}

// TestShellBackend_MultilineScriptFailsAtFirstError is the regression test for
// the original defect: with no shell flags, a multi-line script ran past its
// failing line and the run inherited the *last* command's exit code, so a
// broken script was persisted as a successful run.
func TestShellBackend_MultilineScriptFailsAtFirstError(t *testing.T) {
	out, exit := runScript(t, &model.ShellExecution{Script: "echo one\nnosuchcmd_runwisp\necho two\n"})

	assert.Equal(t, 127, exit, "the run must fail with the failing command's exit code")
	assert.Contains(t, out, "one", "output before the failure is still captured")
	assert.NotContains(t, out, "two", "execution must stop at the first failing command")
}

// TestShellBackend_SingleLineScriptExitCodeUnchanged pins the common case:
// errexit only decides whether execution *continues*, so a one-command script
// behaves exactly as it did before.
func TestShellBackend_SingleLineScriptExitCodeUnchanged(t *testing.T) {
	for _, tc := range []struct {
		script string
		want   int
	}{
		{"echo hi", 0},
		{"exit 3", 3},
		{"true", 0},
		{"false", 1},
	} {
		_, exit := runScript(t, &model.ShellExecution{Script: tc.script})
		assert.Equal(t, tc.want, exit, "script %q", tc.script)
	}
}

// TestShellBackend_SetPlusEOptsOutOfFailFast proves the documented opt-out
// works, which is why fail-fast needs no TOML key: a runtime `set +e` overrides
// the argv flag.
func TestShellBackend_SetPlusEOptsOutOfFailFast(t *testing.T) {
	out, exit := runScript(t, &model.ShellExecution{Script: "set +e\nfalse\necho continued\n"})

	assert.Equal(t, 0, exit)
	assert.Contains(t, out, "continued")
}

// TestShellBackend_ScriptTextNotRewritten guards against someone "simplifying"
// shellArgs into a `set -e` line prepended to the script. Prepending shifts
// every shell diagnostic's line number by one, which quietly misdirects the
// operator reading a failed run's log.
func TestShellBackend_ScriptTextNotRewritten(t *testing.T) {
	script := "#!/usr/bin/env bash\necho hi\n"

	args := shellArgs("/bin/sh", script)

	require.Equal(t, []string{"-e", "-c", script}, args)
	assert.Equal(t, script, args[2], "the executed script must be byte-identical to the TOML")
}

// TestShellBackend_ErrexitCoversUmaskLine checks the interaction with the umask
// wrapper: the mask still applies, the script text gains no `set -e` line, and
// because -e is armed for the whole invocation a failing `umask` can no longer
// vanish.
func TestShellBackend_ErrexitCoversUmaskLine(t *testing.T) {
	def := &model.ShellExecution{Umask: "0027", Script: "umask\n"}

	out, exit := runScript(t, def)

	assert.Equal(t, 0, exit)
	assert.Contains(t, out, "027")
	assert.NotContains(t, wrapScriptUmask(def.Umask, def.Script), "set -e",
		"errexit is passed in argv, never written into the script")
}

// TestShellBackend_NonPosixShellRunsWithoutErrexitFlag proves the gate actually
// gates. A fake interpreter echoes its own argv, so this stays hermetic and
// needs no python/perl on the box.
func TestShellBackend_NonPosixShellRunsWithoutErrexitFlag(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "myinterp")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\necho \"argv: $*\"\n"), 0o755))

	out, exit := runScript(t, &model.ShellExecution{Shell: fake, Script: "print(1)"})

	assert.Equal(t, 0, exit)
	assert.Equal(t, "argv: -c print(1)\n", out, "an unrecognised interpreter must not be handed -e")
}

// TestShellArgs_DefaultShellMatchesConfig pins the duplicated default. The
// executor cannot import config, so this is what keeps the two in step.
func TestShellArgs_DefaultShellMatchesConfig(t *testing.T) {
	assert.Equal(t, config.DefaultShell, defaultShell)
}
