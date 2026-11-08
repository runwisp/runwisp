// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package bootid

import "os"

// Current returns this boot's identity. See fromProc.
func Current() string { return fromProc(os.ReadFile) }

// Process returns the identity of process pid, or "" when it doesn't exist or
// can't be read. See processFromProc.
func Process(pid int) string { return processFromProc(os.ReadFile, pid) }
