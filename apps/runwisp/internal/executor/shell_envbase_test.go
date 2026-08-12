// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package executor

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envNames runs `env` in the child and returns the variable names it saw. Going
// through a real child is the point: the interesting failure is cmd.Env being
// left nil, which no assertion on buildProcessEnv's return value would catch.
func envNames(t *testing.T, exec *model.ShellExecution) []string {
	t.Helper()
	exec.Script = "env"
	out := drainStdout(t, &model.Task{Name: "envbase"}, exec)
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if key, _, ok := strings.Cut(line, "="); ok && key != "" {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	return names
}

// TestShellBackend_CleanEnvBaseDropsTheDaemonsVariables is the whole point of
// the key: a variable the daemon happens to have been started with must not
// reach a task that asked for a clean base.
func TestShellBackend_CleanEnvBaseDropsTheDaemonsVariables(t *testing.T) {
	t.Setenv("RUNWISP_ENVBASE_PROBE", "leaked")

	names := envNames(t, &model.ShellExecution{EnvBase: model.EnvBaseClean})
	assert.NotContains(t, names, "RUNWISP_ENVBASE_PROBE",
		"a clean base must not carry the daemon's environment into the run")
	// PATH is the one that actually breaks jobs when it goes missing, so it is
	// asserted by value rather than presence.
	assert.Contains(t, names, "PATH")
	assert.Contains(t, names, "SHELL")
}

// TestShellBackend_CleanEnvBaseUsesCronsPath pins the value, not just the key.
// Inheriting the daemon's PATH under a "clean" base would look fine in every
// test that only checks PATH is set, and would silently defeat the isolation.
func TestShellBackend_CleanEnvBaseUsesCronsPath(t *testing.T) {
	t.Setenv("PATH", "/daemon/only:"+os.Getenv("PATH"))

	exec := &model.ShellExecution{EnvBase: model.EnvBaseClean}
	exec.Script = `printf '%s' "$PATH"`
	got := drainStdout(t, &model.Task{Name: "envbase"}, exec)
	assert.Equal(t, cleanEnvPath, strings.TrimSpace(got))
}

// TestShellBackend_CleanEnvBaseIsOverriddenByTaskEnv: the base is a floor, not a
// cage. A crontab's own `PATH=` is imported as task env and has to win, or every
// imported job that set one would regress to /usr/bin:/bin.
func TestShellBackend_CleanEnvBaseIsOverriddenByTaskEnv(t *testing.T) {
	task := &model.Task{Name: "envbase", Env: map[string]string{"PATH": "/opt/tools/bin"}}
	got := drainStdout(t, task, &model.ShellExecution{
		Script:  `printf '%s' "$PATH"`,
		EnvBase: model.EnvBaseClean,
	})
	assert.Equal(t, "/opt/tools/bin", strings.TrimSpace(got))
}

// TestShellBackend_InheritEnvBaseKeepsTheDaemonsVariables is the default, and
// the regression this key must not cause: nothing changes for a task that
// didn't ask.
func TestShellBackend_InheritEnvBaseKeepsTheDaemonsVariables(t *testing.T) {
	t.Setenv("ENVBASE_PROBE", "inherited")

	for _, base := range []model.EnvBase{model.EnvBaseInherit, ""} {
		names := envNames(t, &model.ShellExecution{EnvBase: base})
		assert.Contains(t, names, "ENVBASE_PROBE",
			"env_base %q must inherit the daemon's environment", base)
	}
}

// TestShellBackend_CleanEnvBaseStillHidesDaemonSecrets guards the intersection
// of two rules: RUNWISP_* is stripped from the inherited base, and a clean base
// replaces that base entirely. The second must not quietly reintroduce the
// first's job.
func TestShellBackend_CleanEnvBaseStillHidesDaemonSecrets(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "hunter2")

	names := envNames(t, &model.ShellExecution{EnvBase: model.EnvBaseClean})
	for _, n := range names {
		require.False(t, strings.HasPrefix(n, "RUNWISP_"),
			"daemon-internal %s reached a clean-base run", n)
	}
}

// TestShellBackend_InheritEnvBaseStillHidesDaemonSecrets is the regression for
// the leak where a plain task (default inherit base, no env/secrets/params, no
// run-as) left cmd.Env nil and inherited the daemon's RUNWISP_* secrets
// verbatim. A diagnostic task printing its environment would then persist the
// admin password / cloud token into browsable logs.
func TestShellBackend_InheritEnvBaseStillHidesDaemonSecrets(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "hunter2")
	t.Setenv("RUNWISP_CLOUD_TOKEN", "cloud-secret")
	t.Setenv("ENVBASE_PROBE", "inherited")

	for _, base := range []model.EnvBase{model.EnvBaseInherit, ""} {
		names := envNames(t, &model.ShellExecution{EnvBase: base})
		for _, n := range names {
			require.False(t, strings.HasPrefix(n, "RUNWISP_"),
				"daemon-internal %s reached a plain inherit-base run", n)
		}
		// A non-secret daemon variable must still pass through — the filter
		// targets RUNWISP_* only, it does not clear the whole environment.
		assert.Contains(t, names, "ENVBASE_PROBE")
	}
}

// TestCleanEnvBaseSetsShellFromTheResolvedInterpreter: crond exports the SHELL
// it will use, so a job that re-execs "$SHELL" gets the same one. The value has
// to be the interpreter actually chosen, not the daemon's own SHELL.
func TestCleanEnvBaseSetsShellFromTheResolvedInterpreter(t *testing.T) {
	got := cleanEnvBase("/bin/bash")
	assert.Contains(t, got, "SHELL=/bin/bash")
	assert.Contains(t, got, "PATH="+cleanEnvPath)
}
