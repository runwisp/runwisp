// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"log/slog"
	"os/user"
	"sort"
	"strings"
)

// daemonEnvPrefix marks daemon-internal environment variables (the admin
// password, cloud token, and any future RUNWISP_* secret). They live in the
// daemon's own process env but must never be inherited by a task child —
// especially not by a run_user privilege drop. buildProcessEnv strips them
// from the parent base so shell tasks match the container/compose backends,
// which build env from task.Env/Secrets only.
const daemonEnvPrefix = "RUNWISP_"

// cleanEnvPath is the PATH an env_base = "clean" run starts with: crond's own
// compiled-in default (_PATH_DEFPATH). Deliberately not the daemon's PATH —
// the whole point of a clean base is that the run doesn't depend on how the
// daemon was launched. A task that needs more sets PATH in its own env, which
// layers over this.
const cleanEnvPath = "/usr/bin:/bin"

// cleanEnvBase is the environment a host shell run with env_base = "clean"
// starts from: the variables crond guarantees a job, and nothing else.
//
// HOME/USER/LOGNAME describe the account the child will actually run as. The
// daemon's own account is the right answer here because a run-as identity, when
// there is one, is layered over this base by the caller and wins.
func cleanEnvBase(shellPath string) []string {
	env := []string{"PATH=" + cleanEnvPath}
	if shellPath != "" {
		env = append(env, "SHELL="+shellPath)
	}
	u, err := user.Current()
	if err != nil {
		// Not fatal: the run is still better off with a clean PATH than with the
		// daemon's whole environment. Loud, though — a job that reads $HOME will
		// behave differently and this is the only warning of it.
		slog.Warn("env_base=clean: cannot resolve the daemon's own account; this run gets no HOME/USER/LOGNAME", "err", err)
		return env
	}
	return append(env, identityEnv(u.Username, u.HomeDir)...)
}

// buildProcessEnv merges KEY=VALUE entries from parent with successive overlay
// maps. Later overlays override earlier ones; parent acts as the initial layer.
// RUNWISP_*-prefixed keys are dropped from the parent base (daemon internals);
// an overlay layer may still set one explicitly. Output is "KEY=VALUE" strings
// sorted by key for deterministic process env.
//
// Variadic so a future per-run env layer (from the REST/UI trigger surface)
// is a single extra argument: buildProcessEnv(os.Environ(), task.Env, task.Secrets, run.Env).
func buildProcessEnv(parent []string, layers ...map[string]string) []string {
	merged := make(map[string]string, len(parent))
	for _, entry := range parent {
		key, value, ok := splitEnvEntry(entry)
		if !ok {
			continue
		}
		if strings.HasPrefix(key, daemonEnvPrefix) {
			continue
		}
		merged[key] = value
	}
	for _, layer := range layers {
		for key, value := range layer {
			merged[key] = value
		}
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = key + "=" + merged[key]
	}
	return out
}

// splitEnvEntry parses a "KEY=VALUE" string. Entries without an '=' are
// dropped: they aren't valid env syntax and the kernel would refuse them
// anyway. An "=" inside the value is kept verbatim.
func splitEnvEntry(entry string) (key, value string, ok bool) {
	idx := strings.IndexByte(entry, '=')
	if idx <= 0 {
		return "", "", false
	}
	return entry[:idx], entry[idx+1:], true
}
