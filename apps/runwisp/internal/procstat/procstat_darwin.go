// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package procstat

import (
	"os/exec"
	"time"
)

// macOS has no /proc, so each tick runs ps; one exec a second is the budget.
const fastInterval = time.Second

func platformReader() reader {
	return func(pgids map[int]struct{}) map[int]groupStat {
		out, err := exec.Command("/bin/ps", "-A", "-o", "pgid=,rss=,time=").Output()
		if err != nil {
			return nil
		}
		return parsePS(out, pgids)
	}
}
