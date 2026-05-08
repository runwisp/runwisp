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
// cap" sentinel that mirrors keep_runs = -1. Negative durations from the
// underlying parser are rejected so the only path to -1 is the token.
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
	if d < 0 {
		return 0, fmt.Errorf("negative duration %q is not allowed; use \"unlimited\" for no cap", raw)
	}
	return d, nil
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

func parseTaskByteSize(taskName, field, raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := ParseByteSize(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s for task %q: %w", field, taskName, err)
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
