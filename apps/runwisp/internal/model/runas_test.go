// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRunUserSpec(t *testing.T) {
	cases := []struct {
		in        string
		wantUser  string
		wantGroup string
	}{
		{"", "", ""},
		{"nobody", "nobody", ""},
		{"1000", "1000", ""},
		{"nobody:nogroup", "nobody", "nogroup"},
		{"1000:1000", "1000", "1000"},
		{"  alice : staff ", "alice", "staff"}, // surrounding space trimmed
	}
	for _, c := range cases {
		u, g, err := ParseRunUserSpec(c.in)
		require.NoErrorf(t, err, "ParseRunUserSpec(%q)", c.in)
		assert.Equalf(t, c.wantUser, u, "user for %q", c.in)
		assert.Equalf(t, c.wantGroup, g, "group for %q", c.in)
	}
}

func TestParseRunUserSpec_Errors(t *testing.T) {
	bad := []string{
		"a:b:c", // too many separators
		":grp",  // empty user
		"user:", // empty group
		":",     // both empty
	}
	for _, in := range bad {
		_, _, err := ParseRunUserSpec(in)
		assert.Errorf(t, err, "ParseRunUserSpec(%q) should error", in)
	}
}
