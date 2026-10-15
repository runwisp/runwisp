// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

func collisionTasks(cron string, names ...string) []model.Task {
	var tasks []model.Task
	for _, n := range names {
		tasks = append(tasks, model.Task{Name: n, Cron: cron})
	}
	return tasks
}

func TestScheduleCollisionWarnings(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, loc)
	jitter := 5 * time.Minute

	t.Run("three at one instant warn", func(t *testing.T) {
		got := scheduleCollisionWarnings(collisionTasks("0 3 * * *", "a", "b", "c"), loc, now)
		require.Len(t, got, 1)
		require.Contains(t, got[0], "3 tasks start at the same second: a, b, c")
		require.Contains(t, got[0], "jitter")
	})

	t.Run("two do not", func(t *testing.T) {
		require.Empty(t, scheduleCollisionWarnings(collisionTasks("0 3 * * *", "a", "b"), loc, now))
	})

	t.Run("a jittered task does not count", func(t *testing.T) {
		tasks := collisionTasks("0 3 * * *", "a", "b", "c")
		tasks[2].Jitter = &jitter
		require.Empty(t, scheduleCollisionWarnings(tasks, loc, now))
	})

	t.Run("held and unscheduled tasks do not count", func(t *testing.T) {
		tasks := collisionTasks("0 3 * * *", "a", "b", "c")
		tasks[2].HeldBy = model.HeldByCron
		tasks = append(tasks, model.Task{Name: "manual"})
		require.Empty(t, scheduleCollisionWarnings(tasks, loc, now))
	})

	t.Run("a per-task timezone moves it off the shared instant", func(t *testing.T) {
		tasks := collisionTasks("0 3 * * *", "a", "b", "c")
		tasks[2].Timezone = "Asia/Tokyo"
		require.Empty(t, scheduleCollisionWarnings(tasks, loc, now))
	})

	t.Run("@every tasks are left out, their phase is not in the config", func(t *testing.T) {
		require.Empty(t, scheduleCollisionWarnings(collisionTasks("@every 5m", "a", "b", "c"), loc, now))
	})

	t.Run("a group inside a bigger one is reported once", func(t *testing.T) {
		tasks := collisionTasks("0 3 * * *", "a", "b", "c")
		tasks = append(tasks, collisionTasks("0 3 * * 1", "d", "e")...) // Mondays
		got := scheduleCollisionWarnings(tasks, loc, now)
		require.Len(t, got, 1)
		require.Contains(t, got[0], "5 tasks start at the same second: a, b, c, d, e")
	})

	t.Run("a long list is cut", func(t *testing.T) {
		got := scheduleCollisionWarnings(collisionTasks("@hourly", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j"), loc, now)
		require.Len(t, got, 1)
		require.Contains(t, got[0], "10 tasks")
		require.Contains(t, got[0], "h and 2 more;")
	})
}
