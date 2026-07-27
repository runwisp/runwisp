// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocalAPISocketPath_UnderDataDir(t *testing.T) {
	t.Parallel()
	got := localAPISocketPath(Flags{DataDir: "/tmp/runwisp-test-localapi"})
	assert.True(t, strings.HasPrefix(got, "/tmp/runwisp-test-localapi"))
	assert.True(t, strings.HasSuffix(got, "runwisp.sock"))
}

func TestLocalAPISocketPath_ExplicitSocketWins(t *testing.T) {
	t.Parallel()
	// --socket overrides the data-dir default, so a CLI can reach a daemon
	// without restating --data.
	got := localAPISocketPath(Flags{DataDir: "/tmp/ignored", Socket: "/run/rw.sock"})
	assert.Equal(t, "/run/rw.sock", got)
}
