// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
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
