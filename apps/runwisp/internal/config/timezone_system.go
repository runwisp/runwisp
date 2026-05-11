// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"strings"
)

// ResolveSystemTimezone returns the host's IANA timezone name, falling back to
// "UTC" when nothing reliable can be detected. Used by ApplyDefaults to fill
// [scheduler] timezone when the operator omitted it.
//
// Detection order:
//  1. The TZ environment variable, when it names a zoneinfo entry.
//  2. /etc/timezone, the conventional one-line file used by Debian/Ubuntu and
//     several container base images.
//  3. The /etc/localtime symlink target — most common on Fedora, Arch, macOS,
//     Alpine when tzdata is installed.
//
// If none of those produce a name we recognise, the function returns "UTC" —
// always safe, never silently wrong, never DST-affected.
func ResolveSystemTimezone() string {
	if name := tzFromEnv(os.Getenv("TZ")); name != "" {
		return name
	}
	if name := tzFromEtcTimezone(); name != "" {
		return name
	}
	if name := tzFromLocaltimeSymlink(); name != "" {
		return name
	}
	return "UTC"
}

func tzFromEnv(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" || v == ":" {
		return ""
	}
	v = strings.TrimPrefix(v, ":")
	if strings.HasPrefix(v, "/") {
		return tzFromZoneinfoPath(v)
	}
	if isPlausibleZoneName(v) {
		return v
	}
	return ""
}

func tzFromEtcTimezone() string {
	data, err := os.ReadFile("/etc/timezone")
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	if isPlausibleZoneName(name) {
		return name
	}
	return ""
}

func tzFromLocaltimeSymlink() string {
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	return tzFromZoneinfoPath(target)
}

// tzFromZoneinfoPath extracts the IANA name from a zoneinfo path like
// "/usr/share/zoneinfo/Europe/Bratislava" or
// "../usr/share/zoneinfo/America/New_York".
func tzFromZoneinfoPath(path string) string {
	const marker = "zoneinfo/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return ""
	}
	name := path[idx+len(marker):]
	if isPlausibleZoneName(name) {
		return name
	}
	return ""
}

// isPlausibleZoneName performs a cheap shape check; it does not validate
// against the actual zoneinfo database. Validate (via time.LoadLocation)
// happens later when the resolved name lands in cfg.Scheduler.Timezone.
func isPlausibleZoneName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '/' || r == '_' || r == '-' || r == '+':
		default:
			return false
		}
	}
	return true
}
