// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// takeoverHarness swaps the install and reload seams for recorders and restores
// them (plus takeoverOpts) afterwards.
type takeoverHarness struct {
	installed  bool
	installErr error

	req      installRequest
	reloads  int
	installs int

	out bytes.Buffer
}

func newTakeoverHarness(t *testing.T, installed bool, installErr error) *takeoverHarness {
	t.Helper()
	h := &takeoverHarness{installed: installed, installErr: installErr}

	prevInstall, prevReload, prevOpts := takeoverInstall, takeoverReload, takeoverOpts
	t.Cleanup(func() {
		takeoverInstall, takeoverReload, takeoverOpts = prevInstall, prevReload, prevOpts
	})

	takeoverInstall = func(_ *cobra.Command, _ Flags, req installRequest) (bool, error) {
		h.installs++
		h.req = req
		return h.installed, h.installErr
	}
	takeoverReload = func(*cobra.Command, Flags) error {
		h.reloads++
		return nil
	}
	return h
}

func (h *takeoverHarness) run(t *testing.T, f Flags) error {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetOut(&h.out)
	cmd.SetErr(&bytes.Buffer{})
	return runTakeover(cmd, f)
}

// liveDaemonFlags describes a data dir a running daemon owns, so
// isDaemonRunning reports true without a real daemon.
func liveDaemonFlags(t *testing.T) Flags {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "daemon.pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0o600))
	return Flags{DataDir: dir}
}

// The whole reason `takeover` exists as its own command rather than an alias:
// masking cron does not change a running daemon's view of the world, and
// `systemctl enable --now` on an already-active unit is a no-op, so without
// this reload the jobs stay held until the operator works out they must reload.
func TestRunTakeover_ReloadsRunningDaemonAfterInstall(t *testing.T) {
	h := newTakeoverHarness(t, true, nil)

	require.NoError(t, h.run(t, liveDaemonFlags(t)))

	assert.Equal(t, 1, h.installs)
	assert.Equal(t, 1, h.reloads, "a running daemon must be reloaded so the hold lifts")
	assert.True(t, h.req.TakeOverCron, "takeover always answers the cron question yes")
	assert.Contains(t, h.out.String(), "Reloading the running daemon")
}

// The daemon systemd just started loaded its config after cron was masked, so
// nothing is held — a reload would be noise, and on the first-install path
// there may be no socket to reload over at all yet.
func TestRunTakeover_NoReloadWhenNoDaemonWasRunning(t *testing.T) {
	h := newTakeoverHarness(t, true, nil)

	require.NoError(t, h.run(t, Flags{DataDir: t.TempDir()}))

	assert.Equal(t, 1, h.installs)
	assert.Zero(t, h.reloads)
}

// An abort (declined overwrite prompt) leaves cron exactly as it was, so
// reloading would be reload-for-nothing — and would tell the operator the
// cutover happened when it did not.
func TestRunTakeover_NoReloadWhenInstallDidNotHappen(t *testing.T) {
	h := newTakeoverHarness(t, false, nil)

	require.NoError(t, h.run(t, liveDaemonFlags(t)))

	assert.Zero(t, h.reloads)
	assert.NotContains(t, h.out.String(), "Reloading")
}

func TestRunTakeover_InstallErrorStopsBeforeReload(t *testing.T) {
	boom := errors.New("cron.service could not be masked")
	h := newTakeoverHarness(t, false, boom)

	require.ErrorIs(t, h.run(t, liveDaemonFlags(t)), boom)
	assert.Zero(t, h.reloads)
}

// takeover forces TakeOverCron on regardless of the flags parsed, so a refusal
// surfaces as an error from the install path rather than quietly downgrading to
// an install that leaves cron running.
func TestRunTakeover_ForcesTakeOverCronOverParsedFlags(t *testing.T) {
	h := newTakeoverHarness(t, true, nil)
	takeoverOpts = installRequest{AllowSkippedCronJobs: true, DryRun: true}

	require.NoError(t, h.run(t, Flags{DataDir: t.TempDir()}))

	assert.True(t, h.req.TakeOverCron)
	assert.True(t, h.req.AllowSkippedCronJobs, "the parsed flags still reach the install path")
	assert.True(t, h.req.DryRun)
}
