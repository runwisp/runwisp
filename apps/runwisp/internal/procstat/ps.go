// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package procstat

import (
	"strconv"
	"strings"
	"time"
)

// parsePS sums `ps -A -o pgid=,rss=,time=` output per requested process group.
// rss is in KiB; ps reports no high-water mark, so the peak is the current RSS.
// Untagged so it is tested on every platform, not only macOS.
func parsePS(out []byte, pgids map[int]struct{}) map[int]groupStat {
	res := make(map[int]groupStat)
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		pgid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		if _, want := pgids[pgid]; !want {
			continue
		}
		kb, err1 := strconv.ParseInt(f[1], 10, 64)
		cpu, err2 := parsePSTime(f[2])
		if err1 != nil || err2 != nil {
			continue
		}
		g := res[pgid]
		g.CPU += cpu
		g.RSS += kb * 1024
		g.Peak += kb * 1024
		res[pgid] = g
	}
	return res
}

// parsePSTime parses ps cumulative CPU time, "[[dd-]hh:]mm:ss[.cc]".
func parsePSTime(s string) (time.Duration, error) {
	var d time.Duration
	if days, rest, found := strings.Cut(s, "-"); found {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, err
		}
		d = time.Duration(n) * 24 * time.Hour
		s = rest
	}
	parts := strings.Split(s, ":")
	secs, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, err
	}
	d += time.Duration(secs * float64(time.Second))
	unit := time.Minute
	for i := len(parts) - 2; i >= 0; i-- {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return 0, err
		}
		d += time.Duration(n) * unit
		unit = time.Hour
	}
	return d, nil
}
