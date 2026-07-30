// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckNonReloadable_AcceptsTaskOnlyChanges proves the gate ignores the
// reloadable surface: differing [defaults]/tasks must not trip it (the task
// diff handles those), only daemon-wide settings do.
func TestCheckNonReloadable_AcceptsTaskOnlyChanges(t *testing.T) {
	old := &config.Config{Scheduler: config.Scheduler{Timezone: "UTC"}}
	updated := &config.Config{Scheduler: config.Scheduler{Timezone: "UTC"}}
	assert.NoError(t, checkNonReloadable(old, updated))
}

func TestCheckNonReloadable_RejectsEachSection(t *testing.T) {
	base := func() *config.Config {
		return &config.Config{Scheduler: config.Scheduler{Timezone: "UTC"}}
	}

	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"daemon", func(c *config.Config) { c.Daemon.ExternalURL = "https://x" }, "[daemon]"},
		{"timezone", func(c *config.Config) { c.Scheduler.Timezone = "America/New_York" }, "[scheduler] timezone"},
		{"storage", func(c *config.Config) { c.Storage.MaxSize = 1 << 20 }, "[storage]"},
		{"notify", func(c *config.Config) { c.Notify.GlobalNotifiers = []string{"slack"} }, "[notify]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated := base()
			tc.mutate(updated)
			err := checkNonReloadable(base(), updated)
			require.Error(t, err, "%s change must be rejected", tc.name)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "runwisp restart")
		})
	}
}

// TestCheckNonReloadable_RUNWISPTLSDoesNotFlapReload proves reload survives
// RUNWISP_TLS disagreeing with the TOML: config.Load re-applies the same env
// override on every call (boot and every reconcile), so two configs loaded
// under the same RUNWISP_TLS resolve to the same [daemon].TLS regardless of
// what their TOML says — the reconcile gate must not see that as a change.
func TestCheckNonReloadable_RUNWISPTLSDoesNotFlapReload(t *testing.T) {
	t.Setenv("RUNWISP_TLS", "off")

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.toml")
	require.NoError(t, os.WriteFile(oldPath, []byte("[daemon]\ntls = \"auto\"\n\n[tasks.t]\nrun = \"/bin/true\"\n"), 0o600))
	newPath := filepath.Join(dir, "new.toml")
	require.NoError(t, os.WriteFile(newPath, []byte("[tasks.t]\nrun = \"/bin/true\"\n"), 0o600))

	oldCfg, err := config.Load(oldPath)
	require.NoError(t, err)
	newCfg, err := config.Load(newPath)
	require.NoError(t, err)

	require.Equal(t, "off", oldCfg.Daemon.TLS)
	require.Equal(t, "off", newCfg.Daemon.TLS)
	assert.NoError(t, checkNonReloadable(oldCfg, newCfg))
}
