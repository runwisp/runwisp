// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart/autostartfake"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart/autostarttest"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/stretchr/testify/require"
)

// These tests never touch the real machine. The whole point of this package is
// that the cutover decision is a pure function of injected evidence — so every
// probe here is a fake, GOOS is a field, and the suite says the same thing on
// darwin as it does on Linux. That is deliberate: the systemd mechanics in
// internal/autostart sit behind //go:build linux, so decision logic that lived
// beside them was only ever exercised in a Linux CI image, which is how
// `takeover` on a box with no runwisp.toml came to be a dead end unnoticed.
//
// Config files are real, under t.TempDir(): the loader is what answers "does
// this config read crontabs", and faking it would assert the mock.

// fixture is a box description: the files on it, and what its units say.
type fixture struct {
	// config, when non-empty, is written to <tmp>/runwisp.toml before Compute.
	config string
	// crontabs are crontab-format files written under <tmp>/cron.d/, and what a
	// scan of this box would find. Keys are basenames.
	crontabs map[string]string

	goos     string
	euid     int
	username string

	cronUnit   string
	cronActive bool
	// unitInstalled also decides what autostart would plan: an installed unit
	// that matches is a PlanNoop, an absent one a PlanInstall. Derived rather
	// than a field of its own because PlanNoop is PlanKind's zero value, so a
	// fixture that spelled it out could not tell "noop" from "unset".
	unitInstalled bool
	// serviceStopped reports an installed unit that is not running, the state
	// `runwisp stop` leaves behind.
	serviceStopped bool
	daemonRunning  bool

	allowSkipped bool
	// scanBlocked seeds CronScan.Blocked, the pre-config refusals.
	scanBlocked []string
}

// build turns a fixture into a Cutover plus the installer it will talk to.
func (fx fixture) build(t *testing.T) (*Cutover, *autostartfake.Installer, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	if fx.config != "" {
		require.NoError(t, os.WriteFile(cfgPath, []byte(fx.config), 0o644))
	}

	for name, body := range fx.crontabs {
		full := filepath.Join(dir, "crontabs", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}

	unitPlan := autostart.Plan{Kind: autostart.PlanInstall}
	if fx.unitInstalled {
		unitPlan = autostart.Plan{Kind: autostart.PlanNoop}
	}
	inst := &autostartfake.Installer{
		CronUnit:   fx.cronUnit,
		CronActive: fx.cronActive,
		Stat:       autostart.Status{Installed: fx.unitInstalled, Running: fx.unitInstalled && !fx.serviceStopped},
		Plan:       unitPlan,
	}

	goos := fx.goos
	if goos == "" {
		goos = "linux"
	}

	c := New(Deps{
		Installer: inst,
		Prompter:  &autostarttest.ScriptedPrompter{},
		Opts: autostart.InstallOptions{
			Binary: "/usr/local/bin/runwisp", Config: cfgPath,
			DataDir: filepath.Join(dir, "data"), Host: "127.0.0.1", Port: 9477, System: true,
		},
		GOOS:     goos,
		Euid:     fx.euid,
		Username: fx.username,
		// A scan of this box: whatever crontabs the fixture wrote, parsed by the
		// real config.ScanCronSources so job counts and skip reasons are the
		// loader's, not a guess.
		Scan: func(_ []string, cfgPath string) config.CronScan {
			scan := config.ScanCronSources([]string{filepath.Join(dir, "crontabs", "*")}, cfgPath)
			scan.Blocked = append(scan.Blocked, fx.scanBlocked...)
			return scan
		},
		Trusted:       func(string) error { return nil },
		DaemonRunning: func() bool { return fx.daemonRunning },
		Reload:        func(context.Context) error { inst.Calls = append(inst.Calls, "reload"); return nil },
		WriteConfig: func(path string, patterns []string) error {
			inst.Calls = append(inst.Calls, "write-config")
			return os.WriteFile(path, []byte(config.CronStarterConfig(patterns)), 0o644)
		},
		WireCron: func(path string, patterns []string) error {
			inst.Calls = append(inst.Calls, "wire-cron")
			return writeWired(path, patterns)
		},
		AllowSkippedCronJobs: fx.allowSkipped,
	})

	return c, inst, cfgPath
}

// blockerKinds returns the plan's blockers as kinds, for order-sensitive asserts.
func blockerKinds(p Plan) []BlockerKind {
	out := make([]BlockerKind, 0, len(p.Blockers))
	for _, b := range p.Blockers {
		out = append(out, b.Kind)
	}
	return out
}

// stepKinds returns the plan's steps as kinds.
func stepKinds(p Plan) []StepKind {
	out := make([]StepKind, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.Kind)
	}
	return out
}

// hasBlocker reports whether the plan carries one of the given kind.
func hasBlocker(p Plan, kind BlockerKind) bool {
	for _, b := range p.Blockers {
		if b.Kind == kind {
			return true
		}
	}
	return false
}
