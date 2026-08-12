// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReassertResets(t *testing.T) {
	base := BaseSGR(ColorText, ColorBg, false)
	assert.NotEmpty(t, base)

	// A mid-line reset (as captured process output emits) is followed by the
	// base SGR so the pane colours are re-applied for the rest of the row.
	in := "\x1b[1m[backup]\x1b[0m starting snapshot"
	out := ReassertResets(in, base)
	assert.Equal(t, "\x1b[1m[backup]\x1b[0m"+base+" starting snapshot", out)

	// Bare \x1b[m form is handled too.
	assert.Equal(t, "x\x1b[m"+base+"y", ReassertResets("x\x1b[my", base))

	// No escape sequences → returned unchanged (fast path).
	assert.Equal(t, "plain text", ReassertResets("plain text", base))

	// Empty base is a no-op.
	assert.Equal(t, in, ReassertResets(in, ""))

	// Every embedded reset is patched.
	got := ReassertResets("\x1b[0ma\x1b[0mb", base)
	assert.Equal(t, 2, strings.Count(got, base))
}
