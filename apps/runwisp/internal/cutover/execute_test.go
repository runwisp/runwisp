// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/configedit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecute_WritesTheConfigBeforeInstallingTheUnit is the ordering that makes
// the whole feature safe: a unit that starts before the config exists would come
// up reading nothing, and cron is masked from inside that install.
func TestExecute_WritesTheConfigBeforeInstallingTheUnit(t *testing.T) {
	c, inst, cfgPath := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	var out bytes.Buffer
	res, err := c.Execute(context.Background(), p, &out)
	require.NoError(t, err)

	assert.Equal(t, []string{"write-config", "install"}, inst.calls)
	assert.True(t, inst.configAtInstall, "the config must exist by the time Install runs")
	assert.Equal(t, cfgPath, res.ConfigWritten)
	assert.True(t, res.ServiceInstalled)
	assert.FileExists(t, cfgPath)
}

// TestExecute_PassesTakeOverCronAndPreConfirmedToTheInstaller pins the two
// options only this package may set: the mask request, and the promise that the
// operator has already seen a superset of the installer's own banner.
func TestExecute_PassesTakeOverCronAndPreConfirmedToTheInstaller(t *testing.T) {
	c, inst, _ := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	require.True(t, p.MasksCron)

	_, err = c.Execute(context.Background(), p, &bytes.Buffer{})
	require.NoError(t, err)

	assert.True(t, inst.installOpts.TakeOverCron)
	assert.True(t, inst.installOpts.PreConfirmed, "the plan was already confirmed; do not ask twice")
}

// TestExecute_NoCronUnitInstallsWithoutRequestingAMask covers the box that has
// crontabs but no cron unit — RunWisp owns those jobs the moment it starts, and
// asking autostart to mask a unit it cannot find would fail the install.
func TestExecute_NoCronUnitInstallsWithoutRequestingAMask(t *testing.T) {
	c, inst, _ := fixture{
		crontabs: map[string]string{"backup": oneJob},
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	require.False(t, p.MasksCron)

	res, err := c.Execute(context.Background(), p, &bytes.Buffer{})
	require.NoError(t, err)

	assert.True(t, res.ServiceInstalled)
	assert.False(t, inst.installOpts.TakeOverCron)
}

// TestExecute_WiresIncludeCronIntoAnExistingConfig is the second config step: the
// operator already has a runwisp.toml, it just doesn't read crontabs yet.
func TestExecute_WiresIncludeCronIntoAnExistingConfig(t *testing.T) {
	c, inst, cfgPath := fixture{
		config:     "[tasks.hello]\nrun = \"echo hi\"\n",
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	res, err := c.Execute(context.Background(), p, &bytes.Buffer{})
	require.NoError(t, err)

	assert.Equal(t, []string{"wire-cron", "install"}, inst.calls)
	assert.True(t, res.IncludeWired)
	assert.Empty(t, res.ConfigWritten, "wiring is not scaffolding")

	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), "include_cron")
	assert.Contains(t, string(body), "echo hi", "the operator's own tasks survive")
}

// TestExecute_SkipsSatisfiedConfigStep: a config that already reads the crontabs
// is not rewritten just because the plan mentions it.
func TestExecute_SkipsSatisfiedConfigStep(t *testing.T) {
	c, inst, cfgPath := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	wired := "[daemon]\ninclude_cron = [\"" +
		filepath.Join(filepath.Dir(cfgPath), "crontabs", "*") + "\"]\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(wired), 0o644))

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	res, err := c.Execute(context.Background(), p, &bytes.Buffer{})
	require.NoError(t, err)

	assert.Equal(t, []string{"install"}, inst.calls)
	assert.Empty(t, res.ConfigWritten)
	assert.False(t, res.IncludeWired)

	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, wired, string(body), "a satisfied step must not touch the file")
}

// TestExecute_ReloadsOnlyWhenADaemonWasAlreadyRunning pins the sampling the old
// cmd_takeover.go did by hand: the reload exists to make a hand-started daemon
// pick up the config the cutover just wrote. An install that started the daemon
// itself already read it.
func TestExecute_ReloadsOnlyWhenADaemonWasAlreadyRunning(t *testing.T) {
	t.Run("running daemon is reloaded last", func(t *testing.T) {
		c, inst, _ := fixture{
			crontabs:      map[string]string{"backup": oneJob},
			cronUnit:      "cron.service",
			cronActive:    true,
			daemonRunning: true,
		}.build(t)

		p, err := c.Compute(context.Background())
		require.NoError(t, err)

		res, err := c.Execute(context.Background(), p, &bytes.Buffer{})
		require.NoError(t, err)

		assert.Equal(t, []string{"write-config", "install", "reload"}, inst.calls)
		assert.True(t, res.Reloaded)
	})

	t.Run("no daemon means no reload", func(t *testing.T) {
		c, inst, _ := fixture{
			crontabs:   map[string]string{"backup": oneJob},
			cronUnit:   "cron.service",
			cronActive: true,
		}.build(t)

		p, err := c.Compute(context.Background())
		require.NoError(t, err)

		res, err := c.Execute(context.Background(), p, &bytes.Buffer{})
		require.NoError(t, err)

		assert.NotContains(t, inst.calls, "reload")
		assert.False(t, res.Reloaded)
	})
}

// TestExecute_AbortAtTheInstallerPromptIsNotAnError: declining leaves the box
// exactly as it was, which is a legitimate outcome rather than a failure. It must
// also stop before the reload — there is nothing to reconcile.
func TestExecute_AbortAtTheInstallerPromptIsNotAnError(t *testing.T) {
	c, inst, _ := fixture{
		crontabs:      map[string]string{"backup": oneJob},
		cronUnit:      "cron.service",
		cronActive:    true,
		daemonRunning: true,
	}.build(t)
	inst.installErr = autostart.ErrAborted

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	var out bytes.Buffer
	res, err := c.Execute(context.Background(), p, &out)
	require.NoError(t, err)

	assert.True(t, res.Aborted)
	assert.False(t, res.ServiceInstalled)
	assert.False(t, res.Reloaded)
	assert.NotContains(t, inst.calls, "reload")
	assert.Contains(t, out.String(), "cron.service is untouched")
}

// TestExecute_InstallErrorStopsBeforeReload: a failed install means the daemon
// never got the new config, and reloading would report success over a broken box.
func TestExecute_InstallErrorStopsBeforeReload(t *testing.T) {
	c, inst, _ := fixture{
		crontabs:      map[string]string{"backup": oneJob},
		cronUnit:      "cron.service",
		cronActive:    true,
		daemonRunning: true,
	}.build(t)
	boom := errors.New("systemctl exploded")
	inst.installErr = boom

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	res, err := c.Execute(context.Background(), p, &bytes.Buffer{})
	require.ErrorIs(t, err, boom)

	assert.False(t, res.Reloaded)
	assert.NotContains(t, inst.calls, "reload")
	assert.False(t, res.ServiceInstalled)
	// The config write did happen, and the Result says so — a partial cutover
	// that under-reported what is on disk is how an operator loses track of
	// which files RunWisp owns.
	assert.NotEmpty(t, res.ConfigWritten)
}

// TestExecute_ConfigThatAppearedWhileAskingIsNeverClobbered: an operator can sit
// at the prompt for an hour. The plan said "there is no config"; if that stopped
// being true, RunWisp writes nothing.
func TestExecute_ConfigThatAppearedWhileAskingIsNeverClobbered(t *testing.T) {
	c, inst, cfgPath := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	require.True(t, p.pending(StepWriteConfig))

	// Someone else authored one in the meantime.
	require.NoError(t, os.WriteFile(cfgPath, []byte("[tasks.mine]\nrun = \"echo mine\"\n"), 0o644))

	_, err = c.Execute(context.Background(), p, &bytes.Buffer{})
	require.Error(t, err)

	var ue *userError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, ue.Title(), "appeared while RunWisp was asking")
	assert.Empty(t, inst.calls, "nothing may be installed or masked")

	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "[tasks.mine]\nrun = \"echo mine\"\n", string(body))
}

// TestExecute_WireConflictNamesTheImportCollision: WireCronInclude's transaction
// already rolled the file back, so the message's job is to say why it wouldn't
// load and which of the two fixes to pick.
func TestExecute_WireConflictNamesTheImportCollision(t *testing.T) {
	c, inst, cfgPath := fixture{
		config:     "[tasks.hello]\nrun = \"echo hi\"\n",
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)
	c.deps.WireCron = func(string, []string) error {
		return &configedit.ConflictError{Err: errors.New("duplicate task \"backup\"")}
	}

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	_, err = c.Execute(context.Background(), p, &bytes.Buffer{})
	require.Error(t, err)

	var ue *userError
	require.ErrorAs(t, err, &ue)
	assert.Contains(t, ue.Title(), "nothing was written")
	assert.Contains(t, ue.Details(), "duplicate task")
	assert.Contains(t, ue.Details(), "import cron")
	assert.Contains(t, ue.Details(), "crontab -r")
	assert.Empty(t, inst.calls, "a config that would not load must not become a masked cron")

	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "include_cron")
}

// TestExecute_RefusesABlockedPlan is belt and braces: every caller checks
// Blocked() first, but executing one anyway would mask cron on a box RunWisp
// cannot schedule for.
func TestExecute_RefusesABlockedPlan(t *testing.T) {
	c, inst, _ := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
		euid:       1000,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	require.True(t, p.Blocked())

	_, err = c.Execute(context.Background(), p, &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot take over cron")
	assert.Empty(t, inst.calls)
}

// TestExecute_PreflightFailureStopsBeforeAnythingIsMasked: the port check is a
// blocker in spirit, but it runs late because it costs a connection.
func TestExecute_PreflightFailureStopsBeforeAnythingIsMasked(t *testing.T) {
	c, inst, _ := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)
	boom := errors.New("port 9477 is held by another process")
	c.deps.Preflight = func(context.Context) (bool, error) { return false, boom }

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	_, err = c.Execute(context.Background(), p, &bytes.Buffer{})
	require.ErrorIs(t, err, boom)
	assert.NotContains(t, inst.calls, "install")
}

// TestExecute_StaleSettingsAreReported: our own running service holds the port
// with the settings it started with, so "installed" alone would overstate it.
func TestExecute_StaleSettingsAreReported(t *testing.T) {
	c, _, _ := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)
	c.deps.Preflight = func(context.Context) (bool, error) { return true, nil }

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	res, err := c.Execute(context.Background(), p, &bytes.Buffer{})
	require.NoError(t, err)

	assert.True(t, res.ServiceInstalled)
	assert.True(t, res.SettingsStale)
}

// TestExecute_NothingToDoBoxIsANoop: re-running a finished cutover must not
// rewrite the config, re-install the unit, or reload anything.
func TestExecute_NothingToDoBoxIsANoop(t *testing.T) {
	c, inst, cfgPath := fixture{
		crontabs:      map[string]string{"backup": oneJob},
		unitInstalled: true,
	}.build(t)
	require.NoError(t, os.WriteFile(cfgPath, []byte("[daemon]\ninclude_cron = [\""+
		filepath.Join(filepath.Dir(cfgPath), "crontabs", "*")+"\"]\n"), 0o644))

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	require.True(t, p.NothingToDo(), "%v", stepKinds(p))

	res, err := c.Execute(context.Background(), p, &bytes.Buffer{})
	require.NoError(t, err)

	assert.Empty(t, inst.calls)
	assert.Equal(t, Result{}, res)
}
