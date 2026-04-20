// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

		{"-1", 0, true},
		{"-5mb", 0, true},
		{"abc", 0, true},
		{"mb", 0, true},
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
