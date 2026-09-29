// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package bootid

import "os"

// Current returns this boot's identity. See fromProc.
func Current() string { return fromProc(os.ReadFile) }
