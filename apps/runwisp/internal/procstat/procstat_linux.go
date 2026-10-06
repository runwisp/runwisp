// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package procstat

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

const fastInterval = 100 * time.Millisecond

// clockTick is USER_HZ, which the kernel fixes at 100 for /proc.
const clockTick = 10 * time.Millisecond

func platformReader() reader {
	return procReader(os.DirFS("/proc"), int64(os.Getpagesize()))
}

// procReader scans every process in fsys (a /proc root) and sums the ones in a
// requested process group.
//
// ponytail: a full /proc scan per tick; walk /proc/<pid>/task/*/children
// instead if hosts with thousands of processes make it show up.
func procReader(fsys fs.FS, pageSize int64) reader {
	return func(pgids map[int]struct{}) map[int]groupStat {
		out := make(map[int]groupStat)
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			return out
		}
		for _, e := range entries {
			name := e.Name()
			if name[0] < '0' || name[0] > '9' {
				continue
			}
			pgrp, p, ok := readPid(fsys, name, pageSize)
			if _, want := pgids[pgrp]; !ok || !want {
				continue
			}
			g := out[pgrp]
			g.CPU += p.CPU
			g.RSS += p.RSS
			g.Peak += p.Peak
			out[pgrp] = g
		}
		return out
	}
}

// readPid reads one process. A process can vanish mid-scan; it then reports
// ok=false and just drops out of this tick.
func readPid(fsys fs.FS, pid string, pageSize int64) (pgrp int, g groupStat, ok bool) {
	b, err := fs.ReadFile(fsys, pid+"/stat")
	if err != nil {
		return 0, g, false
	}
	pgrp, ticks, rssPages, ok := parseStat(b)
	if !ok {
		return 0, g, false
	}
	g.CPU = time.Duration(ticks) * clockTick
	g.RSS = rssPages * pageSize
	g.Peak = g.RSS
	if hwm, ok := readHWM(fsys, pid); ok {
		g.Peak = hwm
	}
	return pgrp, g, true
}

// parseStat pulls the process group, utime+stime and rss out of
// /proc/<pid>/stat. Fields are counted after the last ')' because the command
// name in between may itself contain spaces and parentheses.
func parseStat(b []byte) (pgrp int, ticks, rssPages int64, ok bool) {
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0, 0, 0, false
	}
	// f[0] is field 3 (state), so field n is f[n-3].
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 22 {
		return 0, 0, 0, false
	}
	pgrp, err1 := strconv.Atoi(f[2])
	utime, err2 := strconv.ParseInt(f[11], 10, 64)
	stime, err3 := strconv.ParseInt(f[12], 10, 64)
	rssPages, err4 := strconv.ParseInt(f[21], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return 0, 0, 0, false
	}
	return pgrp, utime + stime, rssPages, true
}

// readHWM returns the process's resident high-water mark (VmHWM) in bytes.
// Kernel threads and zombies have none.
func readHWM(fsys fs.FS, pid string) (int64, bool) {
	b, err := fs.ReadFile(fsys, pid+"/status")
	if err != nil {
		return 0, false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		rest, found := strings.CutPrefix(sc.Text(), "VmHWM:")
		if !found {
			continue
		}
		kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}
