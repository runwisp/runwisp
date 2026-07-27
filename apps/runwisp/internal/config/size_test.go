// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"0", 0, false},
		{"", 0, false},
		{"1024", 1024, false},
		{"100b", 100, false},
		{"10kb", 10 * 1024, false},
		{"100mb", 100 * 1024 * 1024, false},
		{"1gb", 1 << 30, false},
		{"2tb", 2 * (1 << 40), false},
		{"1.5gb", int64(1.5 * float64(1<<30)), false},
		{"  100MB  ", 100 * 1024 * 1024, false},

		// Mixed case without spaces — units are case-insensitive.
		{"1MB", 1 << 20, false},
		{"1mB", 1 << 20, false},
		// Fractional sizes truncate toward zero after multiplying:
		// 1.9 * 2^30 = 2040109465.6 → 2040109465.
		{"1.9gb", 2040109465, false},
		// Leading dot is a valid float.
		{".5gb", int64(0.5 * float64(1<<30)), false},
		// Zero with any unit collapses to 0.
		{"0b", 0, false},
		{"0mb", 0, false},
		// A space between number and unit is tolerated — the unit parser trims
		// the numeric part, so "100 mb" is 100 MB, not an error. Locking current
		// behavior.
		{"100 mb", 100 * 1024 * 1024, false},

		{"-1", 0, true},
		{"-5mb", 0, true},
		{"abc", 0, true},
		{"mb", 0, true},
		// A trailing plural is not a recognized unit.
		{"1gbs", 0, true},

		// ParseFloat accepts these forms, but they must not slip past as a
		// garbage int64 that silently disables the disk-full / retention guards.
		{"infmb", 0, true},
		{"+infmb", 0, true},
		{"nanmb", 0, true},
		{"1e30tb", 0, true},
		{"0x1p1023tb", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseByteSize(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestFormatByteSize(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{1048576, "1 MB"},
		{1073741824, "1 GB"},
		{-1, "-1 B"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatByteSize(tt.input))
		})
	}
}
