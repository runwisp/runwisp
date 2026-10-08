// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package textutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "0ms"},
		{0, "0ms"},
		{300 * time.Millisecond, "300ms"},
		{1500 * time.Millisecond, "1.5s"},
		{12 * time.Second, "12.0s"},
		{61 * time.Second, "1m1s"},
		{3*time.Minute + 4*time.Second, "3m4s"},
		{5 * time.Minute, "5m0s"},
		{time.Hour, "1h0m"},
		{75 * time.Minute, "1h15m"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, FormatDuration(c.in), "FormatDuration(%s)", c.in)
	}
}
