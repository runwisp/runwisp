// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import "syscall"

// maxRSSBytes converts ru_maxrss, which Linux reports in KiB.
func maxRSSBytes(ru *syscall.Rusage) int64 { return ru.Maxrss * 1024 }
