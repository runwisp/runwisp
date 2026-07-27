// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package executor

import (
	"os/user"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func uidString(u uint32) string { return strconv.FormatUint(uint64(u), 10) }

func TestResolveRunAs_CurrentUserByName(t *testing.T) {
	cur, err := user.Current()
	require.NoError(t, err)

	ra, err := resolveRunAs(cur.Username)
	require.NoError(t, err)
	require.NotNil(t, ra)
	assert.Equal(t, cur.Uid, uidString(ra.cred.Uid))
	assert.Equal(t, cur.Gid, uidString(ra.cred.Gid))
	assert.Contains(t, ra.identity, "USER="+cur.Username)
	assert.Contains(t, ra.identity, "LOGNAME="+cur.Username)
	assert.Contains(t, ra.identity, "HOME="+cur.HomeDir)
}

func TestResolveRunAs_NumericUser(t *testing.T) {
	cur, err := user.Current()
	require.NoError(t, err)

	ra, err := resolveRunAs(cur.Uid)
	require.NoError(t, err)
	require.NotNil(t, ra)
	assert.Equal(t, cur.Uid, uidString(ra.cred.Uid))
}

func TestResolveRunAs_ExplicitGroupOverridesPrimary(t *testing.T) {
	cur, err := user.Current()
	require.NoError(t, err)

	ra, err := resolveRunAs(cur.Username + ":" + cur.Gid)
	require.NoError(t, err)
	require.NotNil(t, ra)
	assert.Equal(t, cur.Gid, uidString(ra.cred.Gid))
}

func TestResolveRunAs_UnknownUser(t *testing.T) {
	_, err := resolveRunAs("runwisp-no-such-user-9d3f")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown user")
}

func TestResolveRunAs_UnknownGroup(t *testing.T) {
	cur, err := user.Current()
	require.NoError(t, err)

	_, err = resolveRunAs(cur.Username + ":runwisp-no-such-group-9d3f")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown group")
}

func TestResolveRunAs_MalformedSpec(t *testing.T) {
	_, err := resolveRunAs("a:b:c")
	require.Error(t, err)
}

// A bare numeric uid with no account entry can't determine a primary gid, so it
// must be rejected unless a group is supplied explicitly.
func TestResolveRunAs_NumericNoAccountNeedsGroup(t *testing.T) {
	const orphan = "4000000000" // valid uint32, almost never has a passwd entry
	if _, err := user.LookupId(orphan); err == nil {
		t.Skipf("uid %s unexpectedly has an account entry on this host", orphan)
	}

	_, err := resolveRunAs(orphan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "explicit group")

	ra, err := resolveRunAs(orphan + ":" + orphan)
	require.NoError(t, err)
	require.NotNil(t, ra)
	assert.Equal(t, orphan, uidString(ra.cred.Uid))
	assert.Equal(t, orphan, uidString(ra.cred.Gid))
	// No account entry → supplementary groups cleared, not inherited.
	assert.False(t, ra.cred.NoSetGroups)
	assert.Empty(t, ra.cred.Groups)
}
