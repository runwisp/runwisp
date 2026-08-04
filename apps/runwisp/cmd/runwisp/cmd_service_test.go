// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The service commands replace the root pre-run hook (so they never mkdir a
// data dir the operator hasn't consented to yet), which means every fallback
// the root hook applies has to be repeated in theirs. RUNWISP_PORT was the one
// that wasn't: the port is baked into the unit, so dropping it wrote a unit
// pinned to a port the operator never asked for, serving on one they weren't
// watching.
func TestServicePathPreRun_AppliesPortAndPathEnvFallbacks(t *testing.T) {
	saved := flags
	t.Cleanup(func() { flags = saved })

	t.Setenv("RUNWISP_PORT", "8080")
	t.Setenv("RUNWISP_CONFIG", "/etc/elsewhere/runwisp.toml")
	t.Setenv("RUNWISP_DATA", "/srv/runwisp")
	flags.CfgFile, flags.DataDir = "", ""

	require.NoError(t, servicePathPreRun(newPortTestCmd(9477), nil))

	assert.Equal(t, 8080, flags.Port)
	assert.Equal(t, "/etc/elsewhere/runwisp.toml", flags.CfgFile)
	assert.Equal(t, "/srv/runwisp", flags.DataDir)
}

func TestServicePathPreRun_ExplicitPortWinsOverEnv(t *testing.T) {
	saved := flags
	t.Cleanup(func() { flags = saved })

	t.Setenv("RUNWISP_PORT", "8080")
	cmd := newPortTestCmd(9477)
	require.NoError(t, cmd.Flags().Set("port", "1234"))

	require.NoError(t, servicePathPreRun(cmd, nil))
	assert.Equal(t, 1234, flags.Port)
}

func TestServicePathPreRun_RejectsGarbagePortEnv(t *testing.T) {
	saved := flags
	t.Cleanup(func() { flags = saved })

	t.Setenv("RUNWISP_PORT", "not-a-port")

	err := servicePathPreRun(newPortTestCmd(9477), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RUNWISP_PORT")
}
