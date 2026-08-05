// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux && !darwin

package server

import "github.com/runwisp/runwisp/internal/model"

// populatePlatformSample has no host-metrics backend on unsupported platforms,
// so it reports the daemon's own Go heap and leaves CPU at zero.
func populatePlatformSample(s *model.MetricsSample) {
	populateFallbackSample(s)
}
