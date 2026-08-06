// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/internal/importer"
	"github.com/runwisp/runwisp/internal/textutil"
)

// Render writes the plan block an operator reads before approving anything: what
// was found, what will happen, and the settings it resolved.
//
// The install step's sub-lines come straight from autostart.Step.Description —
// the same strings autostart builds from the systemctl argv it will actually run.
// Nothing here re-derives a systemctl command.
func Render(w io.Writer, p Plan) {
	renderFindings(w, p)

	if len(p.Steps) > 0 {
		fmt.Fprintln(w)
		for i, s := range p.Steps {
			renderStep(w, i+1, s)
		}
	}

	renderSettings(w, p)

	if p.MasksCron && p.pending(StepInstallService) {
		fmt.Fprintf(w, "\nNothing is destructive until RunWisp is in place: %s is stopped and masked only\n"+
			"after the unit exists, and if RunWisp then fails to start, cron is unmasked and\n"+
			"restarted rather than leaving this box with no scheduler.\n", p.Evidence.CronUnit)
	}
	if !p.MasksCron && !p.Blocked() {
		fmt.Fprintln(w, "\nNothing to mask — this host has no cron systemd unit, so RunWisp owns these\n"+
			"jobs the moment it starts.")
	}
}

// renderFindings opens with what the machine sweep found, which is the part an
// operator wants confirmed before anything else. It prints even on a blocked
// plan: someone told "this needs root" still wants to know twelve jobs are
// waiting.
func renderFindings(w io.Writer, p Plan) {
	ev := p.Evidence
	if ev.CronUnit != "" && ev.CronActive {
		fmt.Fprintf(w, "RunWisp is taking over from %s.\n\n", ev.CronUnit)
	}

	switch {
	case ev.Scan.Jobs > 0:
		fmt.Fprintf(w, "Found %s on this box:\n  %s\n",
			textutil.Count(ev.Scan.Jobs, "cron job", "cron jobs"), strings.Join(ev.Sources, ", "))
		if skipped := ev.Scan.Jobs - ev.Scan.Live; skipped > 0 {
			fmt.Fprintf(w, "  (%d of them need a fix first and would not run)\n", skipped)
		}
	case len(ev.ReadFiles) > 0:
		fmt.Fprintf(w, "RunWisp already reads %s.\n", strings.Join(DescribeSources(ev.ReadFiles), ", "))
	default:
		fmt.Fprintln(w, "No cron jobs found on this box.")
	}
}

// renderStep writes one numbered step, with autostart's own sub-steps nested
// under the install. A satisfied step is marked rather than dropped: a plan that
// silently omitted the finished parts would read like a different, smaller
// operation each time it ran, and the operator could not tell "already done"
// from "not going to happen".
func renderStep(w io.Writer, n int, s Step) {
	marker := fmt.Sprintf("%2d.", n)
	if s.Satisfied {
		marker = "  ✓"
	}
	fmt.Fprintf(w, "%s %s\n", marker, s.Detail)

	for _, sub := range s.Install.Steps {
		fmt.Fprintf(w, "       %s\n", sub.Description)
	}
}

// renderSettings restates what would be baked into the unit. Skipped when the
// install is already satisfied — those values are then a description of the unit
// on disk, not of a change.
func renderSettings(w io.Writer, p Plan) {
	if !p.pending(StepInstallService) {
		return
	}
	fmt.Fprintf(w, "\nResolved settings:\n"+
		"  Binary:    %s\n"+
		"  Config:    %s\n"+
		"  Data dir:  %s\n"+
		"  Host:      %s\n"+
		"  Port:      %d\n",
		p.Opts.Binary, p.Opts.Config, p.Opts.DataDir, p.Opts.Host, p.Opts.Port)
}

// PromptQuestion is the one question a cutover asks. It names the unit being
// retired so a bare "Proceed?" can never be the last thing an operator sees
// before cron stops.
func PromptQuestion(p Plan) string {
	if p.MasksCron {
		return fmt.Sprintf("Take over from %s?", p.Evidence.CronUnit)
	}
	return "Install RunWisp as a system service and read these crontabs?"
}

// DescribeOffer is the first-run prompt body: all three effects, stated before
// the operator says yes to any of them.
//
// Boot persistence is not optional in this phrasing. Masking cron in favour of a
// daemon that dies with the operator's terminal would trade double-firing for
// nothing firing at all.
func DescribeOffer(p Plan) string {
	return fmt.Sprintf(`
RunWisp can take over from cron. It will:
  · read %s as RunWisp tasks (nothing rewritten — crontab -e still works)
  · install itself as a system service, so it starts on boot
  · stop and mask %s, so nothing fires twice

Take over from cron?`, JoinSources(p.Evidence.Sources), p.Evidence.CronUnit)
}

// DescribeSources renders matched cron files as a short, friendly list: every
// file under a cron.d directory collapses to one mention of the directory, and a
// spool file names its owner rather than its path.
func DescribeSources(files []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, f := range files {
		switch {
		case f == importer.SystemCrontabPath:
			add(f)
		case importer.IsSystemCrontabPath(f):
			add(filepath.Dir(f))
		default:
			if owner, ok := importer.UserSpoolOwner(f); ok {
				add(owner + "'s crontab")
			} else {
				add(f)
			}
		}
	}
	return out
}

// JoinSources renders DescribeSources' output for prose.
func JoinSources(sources []string) string {
	switch len(sources) {
	case 0:
		return "your crontabs"
	case 1:
		return sources[0]
	default:
		return fmt.Sprintf("%s and %s",
			strings.Join(sources[:len(sources)-1], ", "), sources[len(sources)-1])
	}
}
