// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPrintStartup_NeverLeaksPassword locks the banner's password-disclosure
// surface: the startup display must never print the plaintext value, only an
// instructional hint pointing operators at `runwisp password` or the TUI.
//
// Stderr capture is awkward, so we drive printStartupTo directly. PrintStartup
// is a one-liner around it, so the same guarantee holds for the public API.
func TestPrintStartup_NeverLeaksPassword(t *testing.T) {
	t.Run("ephemeral renders hint and never the value", func(t *testing.T) {
		const secret = "Kj2x9pQ7mN4vL8rT5wYz1c"
		var buf bytes.Buffer
		printStartupTo(&buf, StartupInfo{
			Version:           "0.0.0-test",
			Port:              9477,
			PasswordEphemeral: true,
			Password:          secret,
		})

		out := buf.String()
		assert.Contains(t, out, "Ephemeral password generated in memory",
			"ephemeral mode must surface the locating hint")
		assert.NotContains(t, out, secret,
			"plaintext ephemeral password must never appear in the banner")
		assert.NotContains(t, out, strings.Repeat("•", passwordMaskWidth),
			"bullet mask belongs on the Home page; the banner has no Password field at all")
	})

	t.Run("operator-supplied password produces no hint and no value", func(t *testing.T) {
		const secret = "operator-supplied-secret"
		var buf bytes.Buffer
		printStartupTo(&buf, StartupInfo{
			Version:           "0.0.0-test",
			Port:              9477,
			PasswordEphemeral: false,
			Password:          secret,
		})

		out := buf.String()
		assert.NotContains(t, out, "🔑",
			"the key glyph is reserved for the ephemeral hint")
		assert.NotContains(t, out, "Ephemeral",
			"non-ephemeral mode must not mention an ephemeral password")
		assert.NotContains(t, out, secret,
			"the operator-supplied password must never reach the banner")
	})

	t.Run("empty ephemeral password still renders hint cleanly", func(t *testing.T) {
		var buf bytes.Buffer
		assert.NotPanics(t, func() {
			printStartupTo(&buf, StartupInfo{
				Version:           "0.0.0-test",
				Port:              9477,
				PasswordEphemeral: true,
				Password:          "",
			})
		})

		out := buf.String()
		assert.Contains(t, out, "Ephemeral password generated in memory",
			"empty-string edge case must still print the hint without short-circuiting")
	})
}
