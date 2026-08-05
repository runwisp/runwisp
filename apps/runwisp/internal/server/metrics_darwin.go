// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package server

import (
	"encoding/binary"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/runwisp/runwisp/internal/model"
	"golang.org/x/sys/unix"
)

// macOS has no /proc, so memory and CPU come from sysctl plus a mach VM
// statistics call. Total RAM is hw.memsize; CPU mirrors the Linux path
// (1-minute load average over core count); used memory is total minus the
// reclaimable pages (free + inactive) reported by host_statistics64 — the macOS
// analog of Linux's MemAvailable. golang.org/x/sys/unix exposes the sysctls but
// not the mach call, so we reach host_statistics64 / mach_host_self through
// libSystem the same cgo-free way x/sys/unix does internally (works with
// CGO_ENABLED=0 because darwin always links libSystem dynamically).
//
// ponytail: free+inactive is a coarse "available" heuristic (ignores purgeable
// and file-backed reclaim); swap the formula if the number reads off vs
// Activity Monitor.

const (
	// From <mach/host_info.h>: HOST_VM_INFO64 flavor, and its size in
	// integer_t (int32) units. vm_statistics64_data_t is 152 bytes → 38.
	hostVMInfo64      = 4
	hostVMInfo64Count = 38
)

func populatePlatformSample(s *model.MetricsSample) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil || total == 0 {
		populateFallbackSample(s)
		return
	}
	free, inactive, ok := vmPageCounts()
	if !ok {
		populateFallbackSample(s)
		return
	}

	pageSize := uint64(unix.Getpagesize())
	available := (free + inactive) * pageSize
	if available > total {
		available = total
	}
	s.MemTotal = total
	s.MemUsed = total - available
	s.MemUsage = float64(s.MemUsed) / float64(total) * 100

	s.CPUUsage = loadCPUPercent()
}

// loadCPUPercent reads the 1-minute load average via sysctl vm.loadavg and
// expresses it as a percentage of the core count, capped at 100 — identical to
// the Linux path. The sysctl returns a struct loadavg { fixpt_t ldavg[3]; long
// fscale; } (little-endian on both amd64 and arm64); load = ldavg[0] / fscale.
func loadCPUPercent() float64 {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(raw) < 16 {
		return 0
	}
	ldavg0 := binary.LittleEndian.Uint32(raw[0:4])

	var fscale uint64
	switch {
	case len(raw) >= 24: // 64-bit long, 8-byte aligned after the uint32[3]
		fscale = binary.LittleEndian.Uint64(raw[16:24])
	default:
		fscale = uint64(binary.LittleEndian.Uint32(raw[12:16]))
	}
	if fscale == 0 {
		return 0
	}

	load1 := float64(ldavg0) / float64(fscale)
	usage := (load1 / float64(runtime.NumCPU())) * 100
	if usage > 100 {
		usage = 100
	}
	return usage
}

// vmPageCounts returns the free and inactive page counts from the mach VM
// statistics. ok is false if the mach calls fail, so the caller can degrade to
// the heap fallback.
func vmPageCounts() (free, inactive uint64, ok bool) {
	host, _, _ := syscallSyscall6(machHostSelfTrampolineAddr, 0, 0, 0, 0, 0, 0)
	if host == 0 {
		return 0, 0, false
	}

	// vm_statistics64_data_t's leading fields are natural_t (uint32):
	// [0]=free_count, [1]=active_count, [2]=inactive_count, [3]=wire_count.
	// host_statistics64 writes the full flavor, so the buffer must be full size.
	var stat [hostVMInfo64Count]uint32
	count := uint32(hostVMInfo64Count)
	ret, _, _ := syscallSyscall6(
		hostStatistics64TrampolineAddr,
		host,
		uintptr(hostVMInfo64),
		uintptr(unsafe.Pointer(&stat[0])),
		uintptr(unsafe.Pointer(&count)),
		0, 0,
	)
	if ret != 0 { // non-zero kern_return_t
		return 0, 0, false
	}
	return uint64(stat[0]), uint64(stat[2]), true
}

// libSystem bridge, mirroring golang.org/x/sys/unix's cgo-free scheme. The
// trampoline address symbols are defined in metrics_darwin.s.

//go:linkname syscallSyscall6 syscall.syscall6
func syscallSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:cgo_import_dynamic libc_host_statistics64 host_statistics64 "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_host_self mach_host_self "/usr/lib/libSystem.B.dylib"

var (
	hostStatistics64TrampolineAddr uintptr
	machHostSelfTrampolineAddr     uintptr
)
