// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
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
// accepts day/week suffixes (e.g. "30d", "2w"). The literal token "unlimited"
// (case-insensitive) maps to time.Duration(-1) — the explicit "no time-based
// cap" sentinel. An empty string means "omitted, inherit the default". Zero
// and negative durations are rejected: there is no longer any way to spell
// "unlimited" with a number — operators must use the keyword.
func parseKeepFor(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	if strings.EqualFold(trimmed, "unlimited") {
		return -1, nil
	}
	d, err := str2duration.ParseDuration(trimmed)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("non-positive duration %q is not allowed; use \"unlimited\" for no cap or omit to inherit the default", raw)
	}
	return d, nil
}

// parseLogMaxSize parses a byte size with the extra "unlimited" keyword.
// Empty string means "omitted, inherit the default"; "unlimited" maps to
// int64(-1) (the explicit "no size cap" sentinel). Zero and negative byte
// counts are rejected so `log_max_size = 0` (which the docs once advertised
// as "unbounded" but the loader actually treated as "inherit") can never
// silently pick the wrong meaning.
func parseLogMaxSize(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	if strings.EqualFold(trimmed, "unlimited") {
		return -1, nil
	}
	n, err := ParseByteSize(trimmed)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("non-positive byte size %q is not allowed; use \"unlimited\" for no cap or omit to inherit the default", raw)
	}
	return n, nil
}

// parseKeepRuns interprets a TOML keep_runs value, which may be either a
// positive integer or the string keyword "unlimited". A nil value means the
// key was omitted and the field should inherit defaults. Zero, negative
// integers, and any other string are rejected — the only way to spell "no
// count cap" is the keyword.
func parseKeepRuns(raw any) (int, error) {
	if raw == nil {
		return 0, nil
	}
	switch v := raw.(type) {
	case int64:
		if v <= 0 {
			return 0, fmt.Errorf("must be a positive integer or \"unlimited\"; got %d", v)
		}
		return int(v), nil
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("must be a positive integer or \"unlimited\"; got %d", v)
		}
		return v, nil
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "unlimited") {
			return -1, nil
		}
		return 0, fmt.Errorf("string value must be \"unlimited\"; got %q", v)
	default:
		return 0, fmt.Errorf("must be a positive integer or \"unlimited\"")
	}
}

func parseTaskDuration(taskName, field, raw string) (time.Duration, error) {
	d, err := parseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s for task %q: %w", field, taskName, err)
	}
	return d, nil
}

func parseTaskKeepFor(taskName, raw string) (time.Duration, error) {
	d, err := parseKeepFor(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid keep_for for task %q: %w", taskName, err)
	}
	return d, nil
}

func parseTaskLogMaxSize(taskName, raw string) (int64, error) {
	n, err := parseLogMaxSize(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid log_max_size for task %q: %w", taskName, err)
	}
	return n, nil
}

func parseTaskKeepRuns(taskName string, raw any) (int, error) {
	n, err := parseKeepRuns(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid keep_runs for task %q: %w", taskName, err)
	}
	return n, nil
}

func parseScopedDuration(scope, raw string) (time.Duration, error) {
	d, err := parseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", scope, err)
	}
	return d, nil
}

func parseScopedKeepFor(scope, raw string) (time.Duration, error) {
	d, err := parseKeepFor(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", scope, err)
	}
	return d, nil
}

func parseScopedLogMaxSize(scope, raw string) (int64, error) {
	n, err := parseLogMaxSize(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", scope, err)
	}
	return n, nil
}

func parseScopedKeepRuns(scope string, raw any) (int, error) {
	n, err := parseKeepRuns(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", scope, err)
	}
	return n, nil
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
