// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// render is the whole plan block as one string, which is what an operator reads.
// Blockers are not part of it: they are text on the Blocker values, rendered by
// whichever surface reports them (cmd/runwisp turns them into its error type so a
// blocked take-over exits non-zero), so this asserts them off the plan.
func render(t *testing.T, c *Cutover) (Plan, string) {
	t.Helper()
	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	var out bytes.Buffer
	Render(&out, p)
	return p, out.String()
}

// blockerText is every blocker's title and details, as one string.
func blockerText(p Plan) string {
	var b strings.Builder
	for _, bl := range p.Blockers {
		b.WriteString(bl.Title)
		b.WriteString("\n")
		b.WriteString(bl.Details)
		b.WriteString("\n")
	}
	return b.String()
}

// TestRender_BareBoxShowsFindingsThenEverySideEffect is the output the reported
// dead-end should have been all along: what was found, then what will happen to
// it, in order, with the config write as step one.
func TestRender_BareBoxShowsFindingsThenEverySideEffect(t *testing.T) {
	c, inst, cfgPath := fixture{
		crontabs:   map[string]string{"backup": oneJob, "rotate": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)
	// autostart's own steps, which Render must nest verbatim rather than restate.
	inst.Plan.Steps = []autostart.Step{
		{Action: autostart.ActionWriteUnit, Description: "write /etc/systemd/system/runwisp.service"},
		{Action: autostart.ActionStopCron, Description: "stop cron.service"},
		{Action: autostart.ActionMaskCron, Description: "mask cron.service"},
	}

	_, text := render(t, c)

	assert.Contains(t, text, "RunWisp is taking over from cron.service.")
	assert.Contains(t, text, "Found 2 cron jobs on this box:")
	assert.Contains(t, text, cfgPath, "the config it will write is named")
	assert.Contains(t, text, "write /etc/systemd/system/runwisp.service")
	assert.Contains(t, text, "mask cron.service")
	assert.Contains(t, text, "Resolved settings:")
	assert.Contains(t, text, "Nothing is destructive until RunWisp is in place")
	assert.NotContains(t, text, "Cannot take over cron yet")
}

// TestRender_StepNumbersAreOrderedAndSatisfiedStepsAreMarked: a plan that
// silently dropped its finished parts would read like a smaller operation each
// run, and the operator could not tell "already done" from "not happening".
func TestRender_StepNumbersAreOrderedAndSatisfiedStepsAreMarked(t *testing.T) {
	c, _, cfgPath := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)
	require.NoError(t, os.WriteFile(cfgPath, []byte("[daemon]\ninclude_cron = [\""+
		filepath.Join(filepath.Dir(cfgPath), "crontabs", "*")+"\"]\n"), 0o644))

	_, text := render(t, c)

	assert.Contains(t, text, "  ✓", "the finished config step is marked, not hidden")
	assert.Contains(t, text, " 2.", "the install still gets its own number")
}

// TestRender_NoCronUnitSaysThereIsNothingToMask — otherwise a plan with no
// stop/mask sub-steps looks like it forgot them.
func TestRender_NoCronUnitSaysThereIsNothingToMask(t *testing.T) {
	c, _, _ := fixture{
		crontabs: map[string]string{"backup": oneJob},
	}.build(t)

	_, text := render(t, c)

	assert.Contains(t, text, "Nothing to mask")
	assert.NotContains(t, text, "Nothing is destructive until")
}

// TestRender_BlockedPlanStillReportsWhatWasFound: someone told "this needs root"
// still wants to know twelve jobs are waiting for them.
func TestRender_BlockedPlanStillReportsWhatWasFound(t *testing.T) {
	c, _, _ := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
		euid:       1000,
	}.build(t)

	p, text := render(t, c)
	require.True(t, p.Blocked())

	assert.Contains(t, text, "Found 1 cron job on this box:")
	assert.Contains(t, blockerText(p), "root")
}

// TestRender_IncludeCronMismatchPrintsThePasteableArray. RunWisp will not rewrite
// a list the operator maintains, so the only useful thing to print is the exact
// text they can paste.
func TestRender_IncludeCronMismatchPrintsThePasteableArray(t *testing.T) {
	c, _, _ := fixture{
		config:     "[daemon]\ninclude_cron = [\"/nowhere/*\"]\n",
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, _ := render(t, c)
	require.True(t, hasBlocker(p, BlockerIncludeCronMissesCrontabs))

	assert.Contains(t, blockerText(p), "include_cron = [")
	assert.Contains(t, blockerText(p), "crontabs")
}

// TestRender_NoJobsAnywhereNamesWhereItLooked, so an operator whose jobs live
// somewhere unusual knows why RunWisp found none.
func TestRender_NoJobsAnywhereNamesWhereItLooked(t *testing.T) {
	c, _, _ := fixture{}.build(t)

	p, text := render(t, c)
	require.True(t, hasBlocker(p, BlockerNoCronJobs))

	assert.Contains(t, text, "No cron jobs found on this box.")
	assert.Contains(t, blockerText(p), "crontabs", "the blocker names where it looked")
}

// TestRender_SettingsAreOmittedOnceTheUnitMatches: those numbers would then
// describe the unit on disk, not a change this run is making.
func TestRender_SettingsAreOmittedOnceTheUnitMatches(t *testing.T) {
	c, _, cfgPath := fixture{
		crontabs:      map[string]string{"backup": oneJob},
		unitInstalled: true,
	}.build(t)
	require.NoError(t, os.WriteFile(cfgPath, []byte("[daemon]\ninclude_cron = [\""+
		filepath.Join(filepath.Dir(cfgPath), "crontabs", "*")+"\"]\n"), 0o644))

	p, text := render(t, c)
	require.True(t, p.NothingToDo())

	assert.NotContains(t, text, "Resolved settings:")
}

// TestRender_SaysCronMailStops: cron mailed these jobs' output to MAILTO and
// that mail silently ended at takeover. The plan has to say so and point at
// notifications; a crontab with no MAILTO, or an empty one, must not get the
// line.
func TestRender_SaysCronMailStops(t *testing.T) {
	for _, tc := range []struct {
		name, crontab string
		want          bool
	}{
		{"MAILTO", "MAILTO=ops@example.com\n" + oneJob, true},
		{"no MAILTO", oneJob, false},
		{"MAILTO empty", "MAILTO=\"\"\n" + oneJob, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := fixture{
				crontabs:   map[string]string{"backup": tc.crontab},
				cronUnit:   "cron.service",
				cronActive: true,
			}.build(t)

			_, text := render(t, c)

			if tc.want {
				assert.Contains(t, text, "Cron mail stops")
				assert.Contains(t, text, "https://docs.runwisp.com/notifications/")
			} else {
				assert.NotContains(t, text, "Cron mail stops")
			}
		})
	}
}

// TestPromptQuestion_NamesTheUnitBeingRetired: a bare "Proceed?" must never be
// the last thing an operator sees before cron stops.
func TestPromptQuestion_NamesTheUnitBeingRetired(t *testing.T) {
	assert.Equal(t, "Take over from cron.service?", PromptQuestion(Plan{
		MasksCron: true,
		Evidence:  Evidence{CronUnit: "cron.service"},
	}))
	assert.Equal(t, "Install RunWisp as a system service and read these crontabs?",
		PromptQuestion(Plan{}))
}

// TestDescribeOffer_StatesAllThreeEffects. The first run asks once; every
// consequence has to be in that one question, boot persistence included —
// masking cron for a daemon that dies with the terminal trades double-firing for
// nothing firing at all.
func TestDescribeOffer_StatesAllThreeEffects(t *testing.T) {
	body := DescribeOffer(Plan{
		Evidence: Evidence{
			CronUnit: "cron.service",
			Sources:  []string{"/etc/crontab", "/etc/cron.d"},
		},
	})

	assert.Contains(t, body, "/etc/crontab and /etc/cron.d")
	assert.Contains(t, body, "crontab -e still works")
	assert.Contains(t, body, "starts on boot")
	assert.Contains(t, body, "stop and mask cron.service")
	assert.Contains(t, body, "Take over from cron?")
	assert.NotContains(t, body, "mail")
}

// The first-run offer retires cron too, so it names the mail that stops.
func TestDescribeOffer_SaysCronMailStops(t *testing.T) {
	body := DescribeOffer(Plan{Evidence: Evidence{
		CronUnit: "cron.service",
		Scan:     config.CronScan{Mails: true},
	}})
	assert.Contains(t, body, "stop cron's MAILTO mail")
	assert.Contains(t, body, "https://docs.runwisp.com/notifications/")
}

func TestDescribeSources_CollapsesDirectoriesAndNamesSpoolOwners(t *testing.T) {
	cronDir := filepath.Dir(importer.SystemCronDirGlob())
	spoolDir := importer.UserSpoolDirs()[0]

	got := DescribeSources([]string{
		importer.SystemCrontabPath,
		filepath.Join(cronDir, "backup"),
		filepath.Join(cronDir, "rotate"),
		filepath.Join(spoolDir, "deploy"),
	})

	assert.Equal(t, []string{
		importer.SystemCrontabPath,
		cronDir,
		"deploy's crontab",
	}, got, "one mention per directory, and a spool file names its owner")
}

func TestJoinSources(t *testing.T) {
	assert.Equal(t, "your crontabs", JoinSources(nil))
	assert.Equal(t, "/etc/crontab", JoinSources([]string{"/etc/crontab"}))
	assert.Equal(t, "/etc/crontab and /etc/cron.d",
		JoinSources([]string{"/etc/crontab", "/etc/cron.d"}))
	assert.Equal(t, "/etc/crontab, /etc/cron.d and bob's crontab",
		JoinSources([]string{"/etc/crontab", "/etc/cron.d", "bob's crontab"}))
}

// TestRender_NamesCrontabsWhoseCronTZIsIgnored: on a Debian host the take-over
// keeps a CRON_TZ-carrying job in the system zone, as Debian cron ran it. The
// plan says so, or the operator reads the zone in the file and expects it.
func TestRender_NamesCrontabsWhoseCronTZIsIgnored(t *testing.T) {
	var out bytes.Buffer
	Render(&out, Plan{Evidence: Evidence{
		Scan: config.CronScan{
			Files: []string{"/etc/cron.d/backup", "/etc/crontab"}, Jobs: 2, Live: 2,
			CronTZIgnored: []string{"/etc/cron.d/backup"},
		},
		Sources: []string{"/etc/cron.d", "/etc/crontab"},
	}})
	assert.Contains(t, out.String(), "(CRON_TZ in /etc/cron.d is ignored, as this host's cron ignores it)")

	out.Reset()
	Render(&out, Plan{Evidence: Evidence{Scan: config.CronScan{Jobs: 1, Live: 1}}})
	assert.NotContains(t, out.String(), "CRON_TZ")
}
