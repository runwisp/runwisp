// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

const (
	// collisionThreshold is how many un-jittered tasks sharing one instant
	// counts as a pile-up. Two jobs at midnight are routine; three starting
	// together is where a small VPS or Pi starts to feel it.
	collisionThreshold = 3
	// collisionHorizon is how far ahead schedules are compared. A week covers
	// every weekly job meeting the daily ones.
	collisionHorizon = 7 * 24 * time.Hour
	// collisionMaxFirings bounds the walk per task.
	// ponytail: a task firing more than ~2000 times a week is only compared over
	// the start of the window; raise the cap if that ever hides a real pile-up.
	collisionMaxFirings = 2016
	// collisionNamesShown caps the task names listed in one warning.
	collisionNamesShown = 8
)

// scheduleCollisionWarnings reports groups of collisionThreshold or more
// schedulable tasks that fire at the same second within collisionHorizon and
// have no jitter to spread them. Pure: the clock is passed in so the same
// config and instant always give the same answer.
//
// Each distinct group is reported once, at its earliest shared instant; a
// group wholly contained in a larger one is dropped, since the larger warning
// already names it.
func scheduleCollisionWarnings(tasks []model.Task, defaultLoc *time.Location, now time.Time) []string {
	if defaultLoc == nil {
		defaultLoc = time.Local
	}
	names, at := firingInstants(tasks, defaultLoc, now)
	var warnings []string
	for _, g := range collisionGroups(at) {
		listed := make([]string, 0, len(g.who))
		for _, idx := range g.who {
			listed = append(listed, names[idx])
		}
		more := ""
		if len(listed) > collisionNamesShown {
			more = fmt.Sprintf(" and %d more", len(listed)-collisionNamesShown)
			listed = listed[:collisionNamesShown]
		}
		warnings = append(warnings, fmt.Sprintf(
			"%d tasks fire at the same instant (next: %s): %s%s; set jitter on them (e.g. jitter = \"5m\") so they take turns instead of starting together",
			len(g.who), cronspec.FormatInstant(time.Unix(g.first, 0).In(defaultLoc)), strings.Join(listed, ", "), more))
	}
	return warnings
}

// firingInstants lists the un-jittered schedulable tasks (names, in config
// order) and, per unix second, which of them fire then within the horizon.
func firingInstants(tasks []model.Task, defaultLoc *time.Location, now time.Time) (names []string, at map[int64][]int) {
	at = map[int64][]int{}
	for i := range tasks {
		task := &tasks[i]
		if !task.Schedulable() || task.JitterValue() > 0 {
			continue
		}
		spec, loc := cronspec.Resolve(task.Cron, task.Timezone, defaultLoc)
		sched, err := cronspec.NewParser().Parse(spec)
		if err != nil {
			continue // Load already rejected it.
		}
		idx := len(names)
		names = append(names, task.Name)
		n := 0
		for f := range cronspec.Firings(sched, loc, now) {
			if f.At.After(now.Add(collisionHorizon)) || n == collisionMaxFirings {
				break
			}
			n++
			if !f.Skipped {
				at[f.At.Unix()] = append(at[f.At.Unix()], idx)
			}
		}
	}
	return names, at
}

// collisionGroup is a set of tasks (indexes, ascending) and the earliest
// second they share.
type collisionGroup struct {
	first int64
	who   []int
}

// collisionGroups picks, from the per-second firing sets, each distinct set of
// collisionThreshold or more tasks once (at its earliest instant), drops sets
// contained in a larger one, and orders the rest by instant.
func collisionGroups(at map[int64][]int) []*collisionGroup {
	byWho := map[string]*collisionGroup{}
	for sec, who := range at {
		who = slices.Compact(who) // @every can tick twice in one second
		if len(who) < collisionThreshold {
			continue
		}
		key := fmt.Sprint(who)
		if g, ok := byWho[key]; !ok || sec < g.first {
			byWho[key] = &collisionGroup{first: sec, who: who}
		}
	}
	var groups []*collisionGroup
	for _, g := range byWho {
		if !coveredByLarger(g, byWho) {
			groups = append(groups, g)
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].first != groups[j].first {
			return groups[i].first < groups[j].first
		}
		return slices.Compare(groups[i].who, groups[j].who) < 0
	})
	return groups
}

// coveredByLarger reports whether some other group names every task in g.
func coveredByLarger(g *collisionGroup, all map[string]*collisionGroup) bool {
	for _, other := range all {
		if len(other.who) > len(g.who) && isSubset(g.who, other.who) {
			return true
		}
	}
	return false
}

// isSubset reports whether every element of small (sorted) is in big (sorted).
func isSubset(small, big []int) bool {
	for _, v := range small {
		if _, found := slices.BinarySearch(big, v); !found {
			return false
		}
	}
	return true
}
