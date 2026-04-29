// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package version exposes the daemon's release version. The default value
// is overridden at build time via ldflags by apps/runwisp/scripts/metadata.sh,
// which parses CHANGELOG.md.
package version

var Version = "0.0.0-dev"
