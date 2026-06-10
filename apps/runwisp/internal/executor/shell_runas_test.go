// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package executor

import (
	"context"
	"os"
	"os/user"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShellBackend_RunAsWithoutPrivilegeFailsLoud is the non-root regression
// guard: requesting a uid other than the daemon's own fails loudly at start
// with a privilege hint, never a silent fallback to the daemon's identity.
func TestShellBackend_RunAsWithoutPrivilegeFailsLoud(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: dropping to another uid would succeed")
	}
	backend := &ShellBackend{}
	task := &model.Task{Name: "ra", RunUser: "0"} // drop to root from non-root → EPERM
	proc, err := backend.Start(context.Background(), task, nil, &model.ShellExecution{Script: "echo hi"})
	require.Error(t, err)
	assert.Nil(t, proc)
	assert.Contains(t, err.Error(), "must run as root")
}

// TestShellBackend_RunAsUnknownUserFails confirms a resolution failure surfaces
// as a start error rather than running as the daemon's user.
func TestShellBackend_RunAsUnknownUserFails(t *testing.T) {
	backend := &ShellBackend{}
	task := &model.Task{Name: "ra", RunUser: "runwisp-no-such-user-9d3f"}
	proc, err := backend.Start(context.Background(), task, nil, &model.ShellExecution{Script: "echo hi"})
	require.Error(t, err)
	assert.Nil(t, proc)
	assert.Contains(t, err.Error(), "resolve run-as")
}

// TestShellBackend_RunAsDropsToTargetUser is the root-gated positive path: the
// child process actually runs as the target user's uid.
func TestShellBackend_RunAsDropsToTargetUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to drop privileges")
	}
	target, err := user.Lookup("nobody")
	if err != nil {
		t.Skip("no 'nobody' account on this host")
	}
	task := &model.Task{Name: "ra", RunUser: "nobody"}
	got := drainStdout(t, task, &model.ShellExecution{Script: "id -u"})
	assert.Equal(t, target.Uid, strings.TrimSpace(got))
}
