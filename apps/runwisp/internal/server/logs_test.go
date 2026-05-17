// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseResumeID(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   int64
		wantOK bool
	}{
		{"empty", "", 0, false},
		{"whitespace", "   ", 0, false},
		{"valid positive", "42", 42, true},
		{"zero", "0", 0, true},
		{"negative", "-1", 0, false},
		{"non-numeric", "abc", 0, false},
		{"float", "1.5", 0, false},
		{"leading and trailing whitespace", "  10  ", 10, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := parseResumeID(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, n)
			}
		})
	}
}
