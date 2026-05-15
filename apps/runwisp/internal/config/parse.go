// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	str2duration "github.com/xhit/go-str2duration/v2"
)

// parseDuration parses a human-readable duration accepted by time.ParseDuration
// (e.g. "30m", "2h45m"). An empty string yields zero with no error.
func parseDuration(raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	return time.ParseDuration(raw)
}

// parseKeepFor parses a retention window using the extended syntax that also
// accepts day/week suffixes (e.g. "30d", "2w"). An empty string means
// "omitted, inherit the default". Zero and negative durations are rejected,
// and the legacy "unlimited" / "inherit" keywords are no longer recognised —
// pre-1.0, RunWisp keeps integer config single-typed.
func parseKeepFor(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	if reservedKeyword(trimmed) {
		return 0, fmt.Errorf("keyword %q is no longer accepted; pick a positive duration or omit the field to inherit the default", raw)
	}
	d, err := str2duration.ParseDuration(trimmed)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("non-positive duration %q is not allowed; pick a positive duration or omit the field to inherit the default", raw)
	}
	return d, nil
}

// parseLogMaxSize parses a byte size. Empty string means "omitted, inherit
// the default"; zero and negative byte counts are rejected. Legacy keywords
// ("unlimited", "inherit") are rejected with a hint.
func parseLogMaxSize(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	if reservedKeyword(trimmed) {
		return 0, fmt.Errorf("keyword %q is no longer accepted; pick a positive byte size or omit the field to inherit the default", raw)
	}
	n, err := ParseByteSize(trimmed)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("non-positive byte size %q is not allowed; pick a positive byte size or omit the field to inherit the default", raw)
	}
	return n, nil
}

// parseKeepRuns interprets a TOML keep_runs value. Zero is the omitted-value
// sentinel that ApplyDefaults rewrites to the [defaults] inheritance; any
// positive integer up to KeepRunsCap is accepted. Negative integers are
// rejected. Above-cap values are rejected by validateKeepRuns.
func parseKeepRuns(raw int) (int, error) {
	if raw < 0 {
		return 0, fmt.Errorf("must be a positive integer; got %d", raw)
	}
	return raw, nil
}

func parseScopedByteSize(scope, raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := ParseByteSize(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", scope, err)
	}
	return n, nil
}

// parseExternalURL normalises [daemon] external_url: trims surrounding
// whitespace and trailing slashes, and rejects anything that doesn't parse
// as an absolute http(s) URL. Empty is the supported default — no link line
// is rendered in notifications when unset.
func parseExternalURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	trimmed = strings.TrimRight(trimmed, "/")
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid daemon.external_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid daemon.external_url: must start with http:// or https://")
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid daemon.external_url: missing host")
	}
	return trimmed, nil
}

// reservedKeyword reports whether the given string is one of the legacy
// keywords ("unlimited", "inherit") that callers used to pass for "no cap" or
// "fall back to defaults". RunWisp now rejects these and surfaces a hint.
func reservedKeyword(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "unlimited", "inherit":
		return true
	}
	return false
}
