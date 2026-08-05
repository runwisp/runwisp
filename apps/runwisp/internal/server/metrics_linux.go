// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package server

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// populatePlatformSample reads real host CPU and memory usage from /proc.
// Memory used is total minus MemAvailable; CPU is the 1-minute load average
// expressed as a percentage of the core count, capped at 100.
func populatePlatformSample(s *model.MetricsSample) {
	memTotal, memAvailable := getMemInfo()
	if memTotal > 0 {
		s.MemTotal = memTotal
		s.MemUsed = memTotal - memAvailable
		s.MemUsage = float64(s.MemUsed) / float64(s.MemTotal) * 100
	}

	load1 := getLoadAvg()
	usage := (load1 / float64(runtime.NumCPU())) * 100
	if usage > 100 {
		usage = 100
	}
	s.CPUUsage = usage
}

func getMemInfo() (uint64, uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var total, available uint64
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(parts[1], 10, 64)
		if strings.HasPrefix(parts[0], "MemTotal") {
			total = val * 1024 // kB to B
		} else if strings.HasPrefix(parts[0], "MemAvailable") {
			available = val * 1024
		}
	}
	return total, available
}

func getLoadAvg() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) > 0 {
		val, _ := strconv.ParseFloat(fields[0], 64)
		return val
	}
	return 0
}
