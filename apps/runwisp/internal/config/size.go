// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseByteSize parses a human-readable byte size string (e.g. "100mb", "5gb", "1024").
// Supported units: b, kb, mb, gb, tb (case-insensitive). Plain numbers are bytes.
func ParseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "0" {
		return 0, nil
	}

	type unit struct {
		suffix string
		mult   int64
	}
	// Longest suffix first to avoid "b" matching "gb"
	units := []unit{
		{"tb", 1 << 40},
		{"gb", 1 << 30},
		{"mb", 1 << 20},
		{"kb", 1 << 10},
		{"b", 1},
	}

	for _, u := range units {
		if result, matched, err := parseByteSizeWithUnit(s, u.suffix, u.mult); matched {
			return result, err
		}
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: must be a number with optional unit (b, kb, mb, gb, tb)", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid byte size %q: must be non-negative", s)
	}
	return n, nil
}

// parseByteSizeWithUnit tries to parse s as a float followed by the given unit
// suffix. Returns (result, true, err) when the suffix matches, (0, false, nil)
// when it does not.
func parseByteSizeWithUnit(s, suffix string, mult int64) (int64, bool, error) {
	if !strings.HasSuffix(s, suffix) {
		return 0, false, nil
	}
	numStr := strings.TrimSpace(s[:len(s)-len(suffix)])
	if numStr == "" {
		return 0, true, fmt.Errorf("invalid byte size %q: missing number", s)
	}
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, true, fmt.Errorf("invalid byte size %q: %w", s, err)
	}
	// ParseFloat accepts "inf", "nan", and out-of-range magnitudes. Reject the
	// non-finite forms outright: they would otherwise slip past the n < 0 guard
	// and produce a garbage int64 (float→int64 overflow is platform-dependent),
	// silently disabling or inverting the disk-full and retention guards.
	if math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, true, fmt.Errorf("invalid byte size %q: must be a finite number", s)
	}
	if n < 0 {
		return 0, true, fmt.Errorf("invalid byte size %q: must be non-negative", s)
	}
	product := n * float64(mult)
	if product >= float64(math.MaxInt64) {
		return 0, true, fmt.Errorf("invalid byte size %q: exceeds the maximum supported size", s)
	}
	return int64(product), true, nil
}

// FormatByteSize formats a byte count as a human-readable string.
func FormatByteSize(bytes int64) string {
	if bytes < 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes == 0 {
		return "0 B"
	}

	type threshold struct {
		min  int64
		unit string
	}
	thresholds := []threshold{
		{1 << 40, "TB"},
		{1 << 30, "GB"},
		{1 << 20, "MB"},
		{1 << 10, "KB"},
	}

	for _, t := range thresholds {
		if bytes >= t.min {
			val := float64(bytes) / float64(t.min)
			if val == float64(int64(val)) {
				return fmt.Sprintf("%d %s", int64(val), t.unit)
			}
			return fmt.Sprintf("%.1f %s", val, t.unit)
		}
	}

	return fmt.Sprintf("%d B", bytes)
}
