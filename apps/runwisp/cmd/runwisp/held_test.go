// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestHeldTaskNames_OnlyHeldTasksInListOrder(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "native", Cron: "* * * * *"},
		{Name: "backup", Cron: "0 3 * * *", HeldBy: model.HeldByCron},
		{Name: "logrotate", Cron: "0 4 * * *"},
		{Name: "vacuum", Cron: "0 5 * * *", HeldBy: model.HeldByCron},
	}
	assert.Equal(t, []string{"backup", "vacuum"}, heldTaskNames(tasks))
}

func TestHeldTaskNames_NothingHeld(t *testing.T) {
	assert.Empty(t, heldTaskNames([]model.TaskBrief{{Name: "native"}}))
	assert.Empty(t, heldTaskNames(nil))
}

// Silence is the whole contract when nothing is held: a block or banner that
// fired on an empty list would put a scary paragraph in front of every operator
// who has never touched cron.
func TestPrintHeld_SilentWhenNothingIsHeld(t *testing.T) {
	var block, banner bytes.Buffer
	printHeldBlock(&block, nil, "is running")
	printHeldBanner(&banner, nil, "is running")
	assert.Empty(t, block.String())
	assert.Empty(t, banner.String())
}

// Both surfaces have to carry the two facts an operator acts on: which tasks are
// not running, and the command that makes them run.
func TestPrintHeld_NamesTasksAndTheWayOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		print func(*bytes.Buffer)
	}{
		{"block", func(b *bytes.Buffer) { printHeldBlock(b, []string{"backup", "vacuum"}, "is running") }},
		{"banner", func(b *bytes.Buffer) { printHeldBanner(b, []string{"backup", "vacuum"}, "is running") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			tc.print(&out)
			got := out.String()
			assert.Contains(t, got, "backup, vacuum")
			assert.Contains(t, got, "is running")
			assert.Contains(t, got, "runwisp takeover")
			assert.Contains(t, got, "2 tasks are")
		})
	}
}

func TestPrintHeldBlock_SingularReadsGrammatically(t *testing.T) {
	var out bytes.Buffer
	printHeldBlock(&out, []string{"backup"}, "is running")
	got := out.String()
	assert.Contains(t, got, "1 task is held")
	assert.Contains(t, got, "is not running it")
	assert.NotContains(t, got, "them")
}

// A box migrating off cron can easily have dozens of jobs. The operator needs to
// recognise the shape of what is held, not scroll a wall of names — but the count
// must still be honest about how many were left out.
func TestHeldNameList_CollapsesALongTail(t *testing.T) {
	var names []string
	for i := range 20 {
		names = append(names, fmt.Sprintf("job%d", i))
	}
	got := heldNameList(names)
	assert.Contains(t, got, "job0, job1")
	assert.Contains(t, got, "(+12 more)")
	assert.NotContains(t, got, "job19")
}

func TestHeldNameList_ShowsEverythingUnderTheCap(t *testing.T) {
	assert.Equal(t, "a, b, c", heldNameList([]string{"a", "b", "c"}))
	assert.NotContains(t, heldNameList([]string{"a", "b", "c"}), "more")
}

// cronState is a full clause ("is running"), not a noun phrase, so splicing it in
// as one produced "a system cron daemon is running still owns them" on every
// `runwisp validate` against a box with a live cron. Assert the whole sentence:
// the old test only checked that "is running" appeared somewhere in it.
func TestPrintHeldBlock_JoinsCronStateGrammatically(t *testing.T) {
	for _, cronState := range []string{
		"is running",
		"is enabled and will start on the next boot",
		"looks like it's running (a live pidfile was found)",
	} {
		var out bytes.Buffer
		printHeldBlock(&out, []string{"backup", "vacuum"}, cronState)
		assert.Contains(t, out.String(),
			"a system cron daemon "+cronState+" and still owns them, so RunWisp is not running them:")
	}
}

// `status` reads a task list over the API and has no cron-daemon prose to work
// with; the block still has to make sense without it.
func TestPrintHeldBlock_ReadsWithoutCronState(t *testing.T) {
	var out bytes.Buffer
	printHeldBlock(&out, []string{"backup"}, "")
	got := out.String()
	assert.Contains(t, got, "cron still owns it")
	assert.NotContains(t, got, "a system cron daemon ,")
}
