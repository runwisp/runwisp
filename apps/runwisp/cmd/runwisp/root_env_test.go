// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirstNonEmpty(t *testing.T) {
	assert.Equal(t, "a", firstNonEmpty("a", "b"))
	assert.Equal(t, "b", firstNonEmpty("", "b"))
	assert.Equal(t, "", firstNonEmpty("", ""))
}

// newPortTestCmd builds a minimal cobra.Command with a "port" flag mirroring
// rootCmd's registration, so resolvePortConfig can be exercised without
// depending on package-level init() state or a full CLI invocation.
func newPortTestCmd(defaultPort int) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().IntVarP(&flags.Port, "port", "p", defaultPort, "")
	return cmd
}

func TestResolvePortConfig(t *testing.T) {
	savedPort := flags.Port
	t.Cleanup(func() { flags.Port = savedPort })

	t.Run("flag explicitly set wins over env", func(t *testing.T) {
		t.Setenv("RUNWISP_PORT", "9999")
		cmd := newPortTestCmd(9477)
		require.NoError(t, cmd.Flags().Set("port", "1234"))
		require.NoError(t, resolvePortConfig(cmd))
		assert.Equal(t, 1234, flags.Port)
	})

	t.Run("no env leaves flag default untouched", func(t *testing.T) {
		cmd := newPortTestCmd(9477)
		require.NoError(t, resolvePortConfig(cmd))
		assert.Equal(t, 9477, flags.Port)
	})

	t.Run("valid env overrides unset flag", func(t *testing.T) {
		t.Setenv("RUNWISP_PORT", "8080")
		cmd := newPortTestCmd(9477)
		require.NoError(t, resolvePortConfig(cmd))
		assert.Equal(t, 8080, flags.Port)
	})

	t.Run("garbage env is a user-facing error", func(t *testing.T) {
		t.Setenv("RUNWISP_PORT", "not-a-port")
		cmd := newPortTestCmd(9477)
		err := resolvePortConfig(cmd)
		require.Error(t, err)
		var ufe *userFacingError
		require.ErrorAs(t, err, &ufe)
		assert.Contains(t, err.Error(), "RUNWISP_PORT")
	})

	t.Run("out of range env is a user-facing error", func(t *testing.T) {
		t.Setenv("RUNWISP_PORT", "70000")
		cmd := newPortTestCmd(9477)
		err := resolvePortConfig(cmd)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "RUNWISP_PORT")
	})

	t.Run("blank env is a no-op", func(t *testing.T) {
		t.Setenv("RUNWISP_PORT", "   ")
		cmd := newPortTestCmd(9477)
		require.NoError(t, resolvePortConfig(cmd))
		assert.Equal(t, 9477, flags.Port)
	})
}
