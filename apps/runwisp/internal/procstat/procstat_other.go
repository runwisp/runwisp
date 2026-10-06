// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux && !darwin

package procstat

import "time"

const fastInterval = time.Second

func platformReader() reader {
	return func(map[int]struct{}) map[int]groupStat { return nil }
}
