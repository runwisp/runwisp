// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadLogOutput(t *testing.T) {
	const off = "[tasks.t]\nrun = \"/bin/true\"\n"
	const on = "[daemon]\nlog_output = true\n" + off

	for name, tc := range map[string]struct {
		toml, env string
		want      bool
	}{
		"default off":          {off, "", false},
		"toml on":              {on, "", true},
		"env turns it on":      {off, "true", true},
		"env overrides toml":   {on, "false", false},
		"blank env is ignored": {on, "  ", true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("RUNWISP_LOG_OUTPUT", tc.env)
			cfg, err := Load(writeTOML(t, tc.toml))
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Daemon.LogOutput)
		})
	}

	t.Run("invalid env is rejected", func(t *testing.T) {
		t.Setenv("RUNWISP_LOG_OUTPUT", "sometimes")
		_, err := Load(writeTOML(t, off))
		require.ErrorContains(t, err, "RUNWISP_LOG_OUTPUT")
	})
}
