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
// accepts day/week suffixes (e.g. "30d", "2w").
func parseKeepFor(raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	return str2duration.ParseDuration(raw)
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
