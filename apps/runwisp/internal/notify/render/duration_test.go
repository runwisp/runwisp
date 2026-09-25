// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"testing"
	"time"
)

// Seconds that round up to 60 carry into the next unit instead of printing "60s".
func TestHumanDurationRoundingCarries(t *testing.T) {
	cases := map[time.Duration]string{
		59600 * time.Millisecond:                "1m",
		time.Minute + 59600*time.Millisecond:    "2m",
		59*time.Minute + 59600*time.Millisecond: "1h",
		3*time.Minute + 4*time.Second:           "3m 4s",
		12400 * time.Millisecond:                "12s",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
