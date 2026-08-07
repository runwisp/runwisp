// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package rhythm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRelative(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "just now"},
		{45 * time.Second, "45s ago"},
		{90 * time.Second, "1m ago"},
		{30 * time.Minute, "30m ago"},
		{2 * time.Hour, "2h ago"},
		{25 * time.Hour, "yesterday"},
		{5 * 24 * time.Hour, "5d ago"},
		{45 * 24 * time.Hour, "1mo ago"},
		{2 * 30 * 24 * time.Hour, "2mo ago"},
		{400 * 24 * time.Hour, "1y ago"},
		{3 * 365 * 24 * time.Hour, "3y ago"},
	}
	for _, tt := range tests {
		got := Relative(now.Add(-tt.d), now)
		assert.Equal(t, tt.want, got, "Relative with d=%v", tt.d)
	}
}
