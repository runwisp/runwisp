// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package procstat

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

// stat builds a /proc/<pid>/stat line with the given comm, pgrp, utime, stime
// and rss (pages); every other field is filler.
func stat(comm, pgrp, utime, stime, rss string) string {
	return "1 (" + comm + ") S 1 " + pgrp + " 1 0 -1 0 0 0 0 0 " + utime + " " + stime +
		" 0 0 20 0 1 0 100 1000 " + rss + " 18446744073709551615\n"
}

func TestProcReader(t *testing.T) {
	fsys := fstest.MapFS{
		// Two members of group 100, one with a comm that would fool a naive split.
		"100/stat":   {Data: []byte(stat("sh", "100", "10", "5", "2"))},
		"100/status": {Data: []byte("Name:\tsh\nVmHWM:\t    12 kB\nVmRSS:\t     8 kB\n")},
		"101/stat":   {Data: []byte(stat("my ) (prog", "100", "100", "0", "4"))},
		"101/status": {Data: []byte("Name:\tprog\nVmHWM:\t  1000 kB\n")},
		// Different group: ignored.
		"200/stat": {Data: []byte(stat("other", "200", "999", "999", "999"))},
		// Zombie with no VmHWM: its RSS stands in for the peak.
		"102/stat":   {Data: []byte(stat("z", "100", "0", "0", "1"))},
		"102/status": {Data: []byte("Name:\tz\nState:\tZ\n")},
		"self":       {Data: []byte("not a pid")},
		"103/stat":   {Data: []byte("garbage")},
	}
	got := procReader(fsys, 4096)(map[int]struct{}{100: {}})
	assert.Equal(t, map[int]groupStat{100: {
		CPU:  115 * clockTick,
		RSS:  (2 + 4 + 1) * 4096,
		Peak: 12*1024 + 1000*1024 + 1*4096,
	}}, got)
}

func TestParseStatRejectsShortLine(t *testing.T) {
	_, _, _, ok := parseStat([]byte("1 (x) S 1 2 3"))
	assert.False(t, ok)
}
