// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import "syscall"

// maxRSSBytes converts ru_maxrss, which macOS already reports in bytes.
func maxRSSBytes(ru *syscall.Rusage) int64 { return ru.Maxrss }
