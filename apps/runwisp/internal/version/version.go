// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package version exposes the daemon's release version. The default value
// is overridden at build time via ldflags by apps/runwisp/scripts/metadata.sh,
// which parses CHANGELOG.md.
package version

var Version = "0.0.0-dev"

// Official is "1" only in binaries built by the release workflow (metadata.sh
// with RUNWISP_RELEASE_BUILD=1). Local, CI, e2e and demo builds carry the same
// Version, so screenshots show the real release, but they stay out of the
// update check and `runwisp upgrade`: they aren't an install anyone made.
var Official string

// IsOfficial reports whether this binary came from the release workflow.
func IsOfficial() bool { return Official == "1" }
