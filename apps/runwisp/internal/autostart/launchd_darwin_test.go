// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package autostart

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLaunchdComputePlan_RefusesTakeOverCron is Phase 4's macOS-side half of
// "refuse with a specific reason": com.vix.cron lives under SIP-protected
// /System/Library/LaunchDaemons, so nothing running as the operator's user
// can mask it the way the Linux installer masks a systemd cron unit.
func TestLaunchdComputePlan_RefusesTakeOverCron(t *testing.T) {
	inst := &launchdInstaller{deps: Deps{
		FS:          NewFakeFS(),
		Cmd:         NewFakeRunner(),
		Home:        "/Users/alice",
		User:        "alice",
		Fingerprint: "bright-falcon",
	}}
	opts := InstallOptions{
		Binary:       "/usr/local/bin/runwisp",
		Config:       "/Users/alice/.config/runwisp/runwisp.toml",
		DataDir:      "/Users/alice/Library/Application Support/runwisp",
		Host:         "127.0.0.1",
		Port:         9477,
		TakeOverCron: true,
	}
	_, err := inst.ComputePlan(context.Background(), opts)
	assert.True(t, errors.Is(err, ErrCronTakeoverUnsupported), "got %v", err)
}
