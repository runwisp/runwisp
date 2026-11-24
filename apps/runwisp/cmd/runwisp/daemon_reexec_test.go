// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A restart is only requested when the binary to re-execute can be found;
// otherwise the daemon keeps running instead of shutting down for good.
func TestRequestRestartMarksTheRuntime(t *testing.T) {
	rt := &daemonRuntime{}
	assert.True(t, rt.requestRestart())
	assert.True(t, rt.restart.Load())
}

func TestReexecPathFallsBackToTheRunningBinary(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"definitely-not-on-path-runwisp"}
	path, err := reexecPath()
	require.NoError(t, err)
	assert.NotEmpty(t, path)
}

func TestRestartOutcome(t *testing.T) {
	boom := errors.New("boom")

	rt := &daemonRuntime{}
	require.NoError(t, rt.restartOutcome(nil))
	assert.Equal(t, boom, rt.restartOutcome(boom))

	rt.restart.Store(true)
	assert.Equal(t, errRestartInPlace, rt.restartOutcome(nil))
	assert.Equal(t, boom, rt.restartOutcome(boom), "a failed shutdown is not turned into a restart")
}

func TestReexecPathPrefersPATHLookup(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"sh"}
	path, err := reexecPath()
	require.NoError(t, err)
	assert.Contains(t, path, "sh")
}
