// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package textutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{-time.Second, "0s"},
		{300 * time.Millisecond, "0.3s"},
		{1500 * time.Millisecond, "2s"}, // rounds to nearest second
		{12 * time.Second, "12s"},
		{12400 * time.Millisecond, "12s"},
		{59 * time.Second, "59s"},
		{61 * time.Second, "1m 1s"},
		{2*time.Minute + 30*time.Second, "2m 30s"},
		{3*time.Minute + 4*time.Second, "3m 4s"},
		{5 * time.Minute, "5m"},
		{time.Hour, "1h"},
		{75 * time.Minute, "1h 15m"},
		// Seconds that round up to 60 carry into the next unit instead of printing "60s".
		{59600 * time.Millisecond, "1m"},
		{time.Minute + 59600*time.Millisecond, "2m"},
		{59*time.Minute + 59600*time.Millisecond, "1h"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, FormatDuration(c.in), "FormatDuration(%s)", c.in)
	}
}
