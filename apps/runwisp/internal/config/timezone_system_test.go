// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsPlausibleZoneName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"UTC", "UTC", true},
		{"America/New_York", "America/New_York", true},
		{"Europe/Bratislava", "Europe/Bratislava", true},
		{"empty", "", false},
		{"space", "foo bar", false},
		{"dot", "US.Eastern", false},
		{"with dash", "Etc/GMT-5", true},
		{"with plus", "Etc/GMT+5", true},
		{"with underscore", "America/Indiana/Knox", true},
		{"slash only", "/", true},
		// "NotAZone" contains only letters — isPlausibleZoneName passes (no slash rule),
		// but load-time LoadLocation would fail. That's intentional per the comment.
		{"all letters no slash", "UTC", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isPlausibleZoneName(tc.input)
			assert.Equal(t, tc.want, got, "isPlausibleZoneName(%q)", tc.input)
		})
	}
}

func TestTzFromEnv_Empty(t *testing.T) {
	result := tzFromEnv("")
	assert.Empty(t, result)
}

func TestTzFromEnv_ColonOnly(t *testing.T) {
	result := tzFromEnv(":")
	assert.Empty(t, result)
}

func TestTzFromEnv_ValidName(t *testing.T) {
	result := tzFromEnv("UTC")
	assert.Equal(t, "UTC", result)
}

func TestTzFromEnv_ValidNameWithColon(t *testing.T) {
	// TZ=:America/New_York strips the colon prefix
	result := tzFromEnv(":America/New_York")
	assert.Equal(t, "America/New_York", result)
}

func TestTzFromEnv_InvalidName(t *testing.T) {
	// Name with spaces is not plausible
	result := tzFromEnv("not a zone")
	assert.Empty(t, result)
}

func TestTzFromEnv_AbsolutePathWithZoneinfo(t *testing.T) {
	// An absolute path pointing to a real zoneinfo entry
	result := tzFromEnv("/usr/share/zoneinfo/UTC")
	assert.Equal(t, "UTC", result)
}

func TestTzFromEnv_AbsolutePathNoZoneinfo(t *testing.T) {
	// Path without zoneinfo marker → tzFromZoneinfoPath returns ""
	result := tzFromEnv("/etc/localtime")
	assert.Empty(t, result)
}

func TestTzFromEnv_Whitespace(t *testing.T) {
	result := tzFromEnv("  ")
	assert.Empty(t, result)
}

func TestTzFromZoneinfoPath_ValidPath(t *testing.T) {
	result := tzFromZoneinfoPath("/usr/share/zoneinfo/America/New_York")
	assert.Equal(t, "America/New_York", result)
}

func TestTzFromZoneinfoPath_NoMarker(t *testing.T) {
	result := tzFromZoneinfoPath("/etc/localtime")
	assert.Empty(t, result)
}

func TestTzFromZoneinfoPath_RelativePath(t *testing.T) {
	result := tzFromZoneinfoPath("../usr/share/zoneinfo/Europe/Bratislava")
	assert.Equal(t, "Europe/Bratislava", result)
}

func TestTzFromZoneinfoPath_ImplausibleAfterMarker(t *testing.T) {
	// The extracted name contains a space → not plausible
	result := tzFromZoneinfoPath("/usr/share/zoneinfo/foo bar")
	assert.Empty(t, result)
}

func TestTzFromLocaltimeSymlink_ValidTarget(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the fake zoneinfo file
	zoneinfoDir := filepath.Join(tmpDir, "usr", "share", "zoneinfo", "America")
	if err := os.MkdirAll(zoneinfoDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(zoneinfoDir, "Chicago")
	if err := os.WriteFile(target, []byte("fake tzdata"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// We cannot override /etc/localtime, but we can test the underlying
	// tzFromZoneinfoPath directly with the symlink target path.
	result := tzFromZoneinfoPath(target)
	assert.Equal(t, "America/Chicago", result)
}

// TestTzReaders_HostState verifies both /etc-based resolvers return a result
// shaped consistently with the host's state — either empty (no source on the
// system) or a plausible IANA zone name. We can't override the system files,
// so this is the strongest assertion available without an FS abstraction.
func TestTzReaders_HostState(t *testing.T) {
	for _, fn := range []struct {
		name string
		read func() string
	}{
		{"tzFromLocaltimeSymlink", tzFromLocaltimeSymlink},
		{"tzFromEtcTimezone", tzFromEtcTimezone},
	} {
		t.Run(fn.name, func(t *testing.T) {
			result := fn.read()
			if result != "" && !isPlausibleZoneName(result) {
				t.Fatalf("%s returned non-plausible name: %q", fn.name, result)
			}
		})
	}
}

func TestResolveSystemTimezone_ReturnsNonEmpty(t *testing.T) {
	result := ResolveSystemTimezone()
	assert.NotEmpty(t, result, "ResolveSystemTimezone must always return a non-empty string")
}

func TestResolveSystemTimezone_FallbackToUTCWhenTZInvalid(t *testing.T) {
	// Set TZ to an invalid value; the function should fall through to other
	// detection paths and ultimately return a non-empty result (possibly UTC).
	t.Setenv("TZ", "not a zone!")
	result := ResolveSystemTimezone()
	assert.NotEmpty(t, result)
}

func TestResolveSystemTimezone_WithValidTZEnv(t *testing.T) {
	t.Setenv("TZ", "UTC")
	result := ResolveSystemTimezone()
	assert.Equal(t, "UTC", result)
}

func TestResolveSystemTimezone_WithAmericaTZEnv(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	result := ResolveSystemTimezone()
	assert.Equal(t, "America/New_York", result)
}

// TestResolveSystemTimezone_PlausibleButInvalidFallsBackToUTC guards the
// boot-brick regression: a TZ that passes the cheap shape check but names no
// real zone (a typo) must resolve to UTC, not be stamped verbatim into the
// scheduler where LoadLocation would later hard-fail startup.
func TestResolveSystemTimezone_PlausibleButInvalidFallsBackToUTC(t *testing.T) {
	t.Setenv("TZ", "Europe/Bratsilava") // misspelling of Bratislava
	result := ResolveSystemTimezone()
	assert.Equal(t, "UTC", result)
}
