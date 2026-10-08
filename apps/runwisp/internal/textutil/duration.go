// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package textutil

import (
	"fmt"
	"time"
)

// FormatDuration renders an elapsed time the way an operator reads it aloud:
// "0.3s", "12s", "3m 4s", "1h 12m". Zero or negative durations (clock skew)
// read as "0s". Notification bodies, including the documented webhook
// payload, use this format, so changing it is user-visible.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	// Round before splitting into units so 59.6s carries into "1m", not "60s".
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return joinUnits(int(d/time.Minute), "m", int(d%time.Minute/time.Second), "s")
	}
	return joinUnits(int(d/time.Hour), "h", int(d%time.Hour/time.Minute), "m")
}

func joinUnits(major int, majorUnit string, minor int, minorUnit string) string {
	if minor == 0 {
		return fmt.Sprintf("%d%s", major, majorUnit)
	}
	return fmt.Sprintf("%d%s %d%s", major, majorUnit, minor, minorUnit)
}
