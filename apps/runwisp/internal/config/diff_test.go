// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tasksMap(tasks ...*model.Task) map[string]*model.Task {
	out := make(map[string]*model.Task, len(tasks))
	for _, t := range tasks {
		out[t.Name] = t
	}
	return out
}

func TestDiffTasks_AddedRemoved(t *testing.T) {
	old := tasksMap(&model.Task{Name: "keep", Run: "echo"})
	updated := tasksMap(
		&model.Task{Name: "keep", Run: "echo"},
		&model.Task{Name: "fresh", Run: "echo"},
	)

	d := DiffTasks(old, updated)
	assert.Equal(t, []string{"fresh"}, d.Added)
	assert.Empty(t, d.Removed)
	assert.Empty(t, d.Changed)

	// Reverse: the now-missing task is removed.
	d = DiffTasks(updated, old)
	assert.Equal(t, []string{"fresh"}, d.Removed)
	assert.Empty(t, d.Added)
}

func TestDiffTasks_IdenticalIsEmpty(t *testing.T) {
	old := tasksMap(&model.Task{Name: "a", Run: "echo", Cron: "@daily"})
	updated := tasksMap(&model.Task{Name: "a", Run: "echo", Cron: "@daily"})
	assert.True(t, DiffTasks(old, updated).IsEmpty())
}

// TestDiffTasks_ProvenanceOnlyChangeIsRestamped covers `runwisp promote`: moving a
// task's block from the staging file into the operator's own config flips the
// derived Staged flag and nothing else. That is not a change to what runs, so it
// must not become a Changed entry — the reconciler acts on those by rescheduling
// cron entries and recycling services.
func TestDiffTasks_ProvenanceOnlyChangeIsRestamped(t *testing.T) {
	staged := &model.Task{Name: "a", Run: "echo", Cron: "@daily", Source: model.SourceStaged}
	promoted := *staged
	promoted.Source = model.SourceNative

	d := DiffTasks(tasksMap(staged), tasksMap(&promoted))

	assert.Empty(t, d.Changed)
	assert.Empty(t, d.Added)
	assert.Empty(t, d.Removed)
	assert.Equal(t, []string{"a"}, d.Restamped)
	assert.True(t, d.IsEmpty(), "reload reports no task changes, because nothing the daemon runs changed")
	assert.Empty(t, d.ToResult().Changed, "provenance stays off the reload wire")
}

// TestDiffTasks_RealChangeIsNotRestamped keeps the masking honest: a definition
// that genuinely differs is still a Changed entry, even when its provenance moved
// in the same reload.
func TestDiffTasks_RealChangeIsNotRestamped(t *testing.T) {
	before := &model.Task{Name: "a", Run: "echo old", Cron: "@daily", Source: model.SourceStaged}
	after := &model.Task{Name: "a", Run: "echo new", Cron: "@daily"}

	d := DiffTasks(tasksMap(before), tasksMap(after))

	require.Len(t, d.Changed, 1)
	assert.True(t, d.Changed[0].Has(ReasonCommand))
	assert.Empty(t, d.Restamped)
}

// TestDiffTasks_HoldLiftIsAScheduleChange is the counterpart to the masking
// above, and the reason HeldBy is deliberately left unmasked. Retiring cron and
// reloading changes only this one derived field — if it were treated as
// provenance the reload would be a Restamped no-op, the registry would take the
// new pointer, the scheduler would never be told, and the jobs the operator just
// handed over would go from "held, and visibly so" to silently never running.
func TestDiffTasks_HoldLiftIsAScheduleChange(t *testing.T) {
	held := &model.Task{Name: "a", Run: "echo", Cron: "@daily",
		Source: model.SourceCron, SourceFile: "/etc/cron.d/a", HeldBy: model.HeldByCron}
	freed := *held
	freed.HeldBy = model.HeldByNothing

	d := DiffTasks(tasksMap(held), tasksMap(&freed))

	require.Len(t, d.Changed, 1)
	assert.True(t, d.Changed[0].Has(ReasonSchedule),
		"the reconciler reschedules on this reason; nothing else registers the cron entry")
	assert.Empty(t, d.Restamped)
	assert.False(t, d.IsEmpty(), "a job that starts firing is a change worth reporting")
}

// TestDiffTasks_HoldLandingIsAScheduleChange is the same flip in reverse — cron
// came back (a package upgrade unmasked it) and RunWisp has to drop the entry.
func TestDiffTasks_HoldLandingIsAScheduleChange(t *testing.T) {
	free := &model.Task{Name: "a", Run: "echo", Cron: "@daily",
		Source: model.SourceCron, SourceFile: "/etc/cron.d/a"}
	held := *free
	held.HeldBy = model.HeldByCron

	d := DiffTasks(tasksMap(free), tasksMap(&held))

	require.Len(t, d.Changed, 1)
	assert.True(t, d.Changed[0].Has(ReasonSchedule))
	assert.Empty(t, d.Restamped)
}

func TestDiffTasks_ChangeReasons(t *testing.T) {
	base := func() *model.Task {
		return &model.Task{Name: "a", Run: "echo", Cron: "0 2 * * *", Kind: model.KindTask}
	}

	cases := []struct {
		name   string
		mutate func(*model.Task)
		want   ChangeReason
	}{
		{"cron", func(tk *model.Task) { tk.Cron = "0 3 * * *" }, ReasonSchedule},
		{"timezone", func(tk *model.Task) { tk.Timezone = "America/New_York" }, ReasonSchedule},
		{"kind", func(tk *model.Task) { tk.Kind = model.KindService }, ReasonKind},
		{"command", func(tk *model.Task) { tk.Run = "echo changed" }, ReasonCommand},
		{"workdir", func(tk *model.Task) { tk.WorkingDir = "/tmp" }, ReasonCommand},
		{"env", func(tk *model.Task) { tk.Env = map[string]string{"K": "V"} }, ReasonEnv},
		{"env-file", func(tk *model.Task) { tk.EnvFile = ".env" }, ReasonEnv},
		// A task switched to a clean base runs with a different environment; a
		// reload that reported no change would leave it on the old one until the
		// next restart.
		{"env-base", func(tk *model.Task) { tk.EnvBase = model.EnvBaseClean }, ReasonEnv},
		{"settings", func(tk *model.Task) { tk.MaxConcurrent = 4 }, ReasonSettings},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated := base()
			tc.mutate(updated)
			d := DiffTasks(tasksMap(base()), tasksMap(updated))
			require.Len(t, d.Changed, 1)
			assert.True(t, d.Changed[0].Has(tc.want),
				"expected reason %q in %v", tc.want, d.Changed[0].Reasons)
		})
	}
}

func TestDiffTasks_DeterministicOrder(t *testing.T) {
	old := tasksMap(&model.Task{Name: "stay", Run: "echo"})
	updated := tasksMap(
		&model.Task{Name: "stay", Run: "echo"},
		&model.Task{Name: "zebra", Run: "echo"},
		&model.Task{Name: "alpha", Run: "echo"},
		&model.Task{Name: "mike", Run: "echo"},
	)
	d := DiffTasks(old, updated)
	assert.Equal(t, []string{"alpha", "mike", "zebra"}, d.Added, "Added must be sorted")
}

func TestDiff_ToResult(t *testing.T) {
	d := Diff{
		Added:   []string{"x"},
		Removed: []string{"y"},
		Changed: []TaskChange{{Name: "z", Reasons: []ChangeReason{ReasonSchedule, ReasonEnv}}},
	}
	res := d.ToResult()
	assert.Equal(t, []string{"x"}, res.Added)
	assert.Equal(t, []string{"y"}, res.Removed)
	require.Len(t, res.Changed, 1)
	assert.Equal(t, "z", res.Changed[0].Name)
	assert.Equal(t, []string{"schedule", "env"}, res.Changed[0].Reasons)
	assert.False(t, res.IsEmpty())
}
