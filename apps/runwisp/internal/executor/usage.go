// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"syscall"
	"time"
)

// runUsage turns the kernel's at-exit accounting and the sampled peak into the
// run's persisted totals. CPU time comes straight from rusage. Peak memory
// can't: Linux folds the daemon's RSS high-water mark into every child at
// vfork+exec, so a child's ru_maxrss is floored at the daemon's own. It only
// counts when it beats that floor, which proves it is the run's own peak;
// otherwise the sampled peak stands. Either is nil when unknown.
func runUsage(ru *syscall.Rusage, sampledPeak int64, sampled bool) (peakMemoryBytes, cpuTimeMs *int64) {
	if sampled {
		peakMemoryBytes = &sampledPeak
	}
	if ru == nil {
		return peakMemoryBytes, nil
	}
	cpu := (time.Duration(ru.Utime.Nano()) + time.Duration(ru.Stime.Nano())).Milliseconds()
	var self syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &self) != nil {
		return peakMemoryBytes, &cpu
	}
	return pickPeak(peakMemoryBytes, maxRSSBytes(ru), maxRSSBytes(&self)), &cpu
}

// pickPeak takes the child's ru_maxrss only when it exceeds the daemon's own
// (the floor it may have inherited), and never below the sampled peak.
func pickPeak(sampled *int64, childMaxRSS, selfMaxRSS int64) *int64 {
	if childMaxRSS <= selfMaxRSS {
		return sampled
	}
	if sampled != nil && *sampled > childMaxRSS {
		return sampled
	}
	return &childMaxRSS
}
