// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
)

// explainFirings is how many upcoming firings `validate --explain` lists per
// task. Fall-back repeats the scheduler would drop are listed too, marked, but
// don't count toward it.
const explainFirings = 3

// explainSchedules describes every schedulable task: its cron in words and its
// next firings in the zone the scheduler will evaluate it in, DST effects
// marked. Held tasks are left out (their schedule belongs to cron for now).
func explainSchedules(cfg *config.Config, now time.Time) []scheduleJSON {
	var out []scheduleJSON
	for i := range cfg.Tasks {
		task := &cfg.Tasks[i]
		if !task.Schedulable() {
			continue
		}
		spec, loc := cronspec.Resolve(task.Cron, task.Timezone, cfg.SchedulerLocation())
		sched, err := cronspec.NewParser().Parse(spec)
		if err != nil {
			continue // Load already rejected it.
		}
		s := scheduleJSON{
			Task:        task.Name,
			Cron:        task.Cron,
			Timezone:    loc.String(),
			Description: cronspec.Describe(task.Cron),
			Next:        []firingJSON{},
		}
		if j := task.JitterValue(); j > 0 {
			s.Jitter = textutil.FormatDuration(j)
		}
		listed := 0
		for f := range cronspec.Firings(sched, loc, now) {
			s.Next = append(s.Next, firingJSON{At: f.At.Format(time.RFC3339), Display: cronspec.FormatInstant(f.At), DSTShifted: f.Shifted, DSTSkipped: f.Skipped})
			if !f.Skipped {
				listed++
			}
			if listed == explainFirings {
				break
			}
		}
		out = append(out, s)
	}
	return out
}

func printSchedules(w io.Writer, schedules []scheduleJSON) {
	for _, s := range schedules {
		fmt.Fprintf(w, "\n%s  %s  (%s)\n  %s\n", s.Task, s.Cron, s.Timezone, s.Description)
		if s.Jitter != "" {
			fmt.Fprintf(w, "  starts up to %s late when other tasks are running\n", s.Jitter)
		}
		for _, f := range s.Next {
			note := ""
			switch {
			case f.DSTSkipped:
				note = "  skipped: DST repeat, already ran at this wall-clock time"
			case f.DSTShifted:
				note = "  DST: the scheduled time does not exist that day, runs at this time instead"
			}
			fmt.Fprintf(w, "  %s%s\n", f.Display, note)
		}
	}
}
