// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package autostartfake holds an in-memory autostart.Installer for tests of
// code that drives one (cutover, the service and takeover commands). It lives
// beside autostarttest rather than in it because it needs the autostart types,
// and autostart's own tests import autostarttest.
package autostartfake

import (
	"context"
	"io"
	"os"

	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
)

// Installer answers every autostart.Installer method from its fields and
// records what Install, Restart and the drop-in writers were asked to do. The
// zero value is a box with no unit, no cron unit and drop-in support.
type Installer struct {
	// Answers.
	Body       []byte // Render
	Plan       autostart.Plan
	PlanErr    error
	Stat       autostart.Status
	StatErr    error
	InstallErr error
	CronUnit   string
	CronActive bool
	CronErr    error
	// NoDropIn makes SupportsPasswordDropIn report false (the launchd case).
	NoDropIn    bool
	EnsurePath  string
	EnsureWrote bool
	EnsureErr   error
	EnvPath     string
	EnvChange   autostart.DropInChange
	EnvErr      error
	// OnInstall, when set, runs inside Install (e.g. to leave a live PID file
	// behind, the way `systemctl enable --now` leaves a daemon).
	OnInstall func(autostart.InstallOptions)

	// Records.
	Calls []string // "install", plus whatever a test appends
	// InstallOpts is what Install was last handed.
	InstallOpts autostart.InstallOptions
	// ConfigAtInstall is whether opts.Config existed when Install ran, the
	// ordering a unit pointing at a missing config would break.
	ConfigAtInstall bool
	EnsureCalled    bool
	EnsurePassword  string
	EnvCalled       bool
	EnvVars         map[string]string
	Restarts        int
}

// Installs reports how many times Install ran.
func (f *Installer) Installs() int {
	n := 0
	for _, c := range f.Calls {
		if c == "install" {
			n++
		}
	}
	return n
}

func (f *Installer) Render(autostart.InstallOptions) ([]byte, error) { return f.Body, nil }

func (f *Installer) ComputePlan(context.Context, autostart.InstallOptions) (autostart.Plan, error) {
	return f.Plan, f.PlanErr
}

func (f *Installer) Install(_ context.Context, opts autostart.InstallOptions, _ io.Writer) error {
	f.Calls = append(f.Calls, "install")
	f.InstallOpts = opts
	_, err := os.Stat(opts.Config)
	f.ConfigAtInstall = err == nil
	if f.OnInstall != nil {
		f.OnInstall(opts)
	}
	return f.InstallErr
}

func (f *Installer) Uninstall(context.Context, autostart.UninstallOptions, io.Writer) error {
	return nil
}

func (f *Installer) Status(context.Context, autostart.InstallOptions) (autostart.Status, error) {
	return f.Stat, f.StatErr
}

func (f *Installer) Stop(context.Context, autostart.InstallOptions) error { return nil }

func (f *Installer) Restart(context.Context, autostart.InstallOptions) error {
	f.Restarts++
	return nil
}

func (f *Installer) EnsurePasswordDropIn(_ context.Context, _ autostart.InstallOptions, password string) (string, bool, error) {
	f.EnsureCalled = true
	f.EnsurePassword = password
	return f.EnsurePath, f.EnsureWrote, f.EnsureErr
}

func (f *Installer) SupportsPasswordDropIn() bool { return !f.NoDropIn }

func (f *Installer) WriteEnvDropIn(_ context.Context, _ autostart.InstallOptions, _ string, vars map[string]string) (string, autostart.DropInChange, error) {
	f.EnvCalled = true
	f.EnvVars = vars
	return f.EnvPath, f.EnvChange, f.EnvErr
}

func (f *Installer) CronStatus(context.Context) (string, bool, error) {
	return f.CronUnit, f.CronActive, f.CronErr
}
