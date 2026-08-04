// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

#include "textflag.h"

// Trampolines to the libSystem functions imported via //go:cgo_import_dynamic
// in metrics_darwin.go. Same form x/sys/unix generates; works for amd64 and
// arm64 alike.

TEXT libc_host_statistics64_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_host_statistics64(SB)
GLOBL	·hostStatistics64TrampolineAddr(SB), RODATA, $8
DATA	·hostStatistics64TrampolineAddr(SB)/8, $libc_host_statistics64_trampoline<>(SB)

TEXT libc_mach_host_self_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_host_self(SB)
GLOBL	·machHostSelfTrampolineAddr(SB), RODATA, $8
DATA	·machHostSelfTrampolineAddr(SB)/8, $libc_mach_host_self_trampoline<>(SB)
