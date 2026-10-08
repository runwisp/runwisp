// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package fakeclock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClock_Advance(t *testing.T) {
	start := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	c := New(start)
	assert.True(t, c.Now().Equal(start))
	c.Advance(2 * time.Hour)
	assert.True(t, c.Now().Equal(start.Add(2*time.Hour)))
}

func TestTimers_FireAllSkipsStopped(t *testing.T) {
	var m Timers
	var fired []string
	m.After(time.Second, func() { fired = append(fired, "a") })
	b := m.After(2*time.Second, func() { fired = append(fired, "b") })
	m.After(3*time.Second, func() { fired = append(fired, "c") })

	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}, m.Armed())
	assert.True(t, b.Stop())
	assert.False(t, b.Stop(), "second Stop reports it was already stopped")
	assert.Equal(t, 2, m.Pending())

	m.FireAll()
	assert.Equal(t, []string{"a", "c"}, fired)
	assert.Equal(t, 0, m.Pending())
	m.FireAll()
	assert.Equal(t, []string{"a", "c"}, fired, "fired timers do not fire twice")
}
