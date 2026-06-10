// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
