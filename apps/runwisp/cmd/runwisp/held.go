// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// heldNamesShown caps how many task names the held block lists before collapsing
// the rest into a count. A box migrating off cron can easily have dozens; the
// operator needs to recognise the shape of what is held, not read the whole list.
const heldNamesShown = 8

// heldTaskNames pulls the held tasks out of a task list, preserving its order.
// The daemon derives HeldBy from the same markers the scheduler consults, so this
// can never disagree with what is actually running.
func heldTaskNames(tasks []model.TaskBrief) []string {
	var out []string
	for _, t := range tasks {
		if t.HeldBy != model.HeldByNothing {
			out = append(out, t.Name)
		}
	}
	return out
}

// printHeldBlock writes the held tasks as their own paragraph rather than another
// warning line. Held is not a config problem — RunWisp is deliberately refusing
// to fire jobs cron is already firing — and the operator needs two facts from it:
// which tasks, and the one command that finishes the handover.
//
// cronState is optional prose ("is running") for the daemon still holding them;
// the surfaces that read a live config have it, the ones reading a task list over
// the API do not, and the block reads correctly either way. It is a full clause,
// so it needs the "and" to join what follows — without it the sentence reads
// "a system cron daemon is running still owns them".
func printHeldBlock(w io.Writer, names []string, cronState string) {
	if len(names) == 0 {
		return
	}
	daemon := "cron"
	if cronState != "" {
		daemon = "a system cron daemon " + cronState + " and"
	}
	fmt.Fprintf(w, "\n⏸ %s held — %s still owns %s, so RunWisp is not running %s:\n",
		heldTaskCount(len(names)), daemon, pronounFor(len(names)), pronounFor(len(names)))
	fmt.Fprintf(w, "    %s\n", heldNameList(names))
	fmt.Fprintf(w, "  Run 'sudo runwisp takeover' to hand %s over — or just stop cron, and RunWisp picks %s up.\n",
		pronounFor(len(names)), pronounFor(len(names)))
}

// heldNameList renders the names, collapsing the tail past heldNamesShown.
func heldNameList(names []string) string {
	if len(names) <= heldNamesShown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s (+%d more)",
		strings.Join(names[:heldNamesShown], ", "), len(names)-heldNamesShown)
}

func heldTaskCount(n int) string {
	if n == 1 {
		return "1 task is"
	}
	return fmt.Sprintf("%d tasks are", n)
}

// pronounFor keeps the block grammatical for a single task without building the
// sentence twice.
func pronounFor(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// printHeldBanner is the boot-time form: an unmissable stderr banner beside
// printNoAuthBanner and printNonLoopbackBanner, rather than one slog.Warn among
// the boot INFO lines. It has to survive the spawn-and-echo path, where the TUI's
// alt-screen switch wipes anything that only went to the log.
func printHeldBanner(w io.Writer, names []string, cronState string) {
	if len(names) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\n================================================================================\n")
	fmt.Fprintf(&b, "  HELD: %s not being scheduled by RunWisp.\n", heldTaskCount(len(names)))
	if cronState != "" {
		fmt.Fprintf(&b, "  A system cron daemon %s and still owns the crontabs they came from.\n", cronState)
	}
	b.WriteString("  RunWisp is standing down so nothing fires twice — cron keeps running these\n")
	b.WriteString("  jobs, and RunWisp records no history or output for them.\n")
	fmt.Fprintf(&b, "    %s\n", heldNameList(names))
	b.WriteString("  Run 'sudo runwisp takeover' to hand them over. Or just stop cron: RunWisp\n")
	b.WriteString("  notices within a minute and takes them on, no reload needed.\n")
	b.WriteString("================================================================================\n")
	_, _ = fmt.Fprint(w, b.String())
}
