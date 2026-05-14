// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package home

import (
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
)

// TestRenderHomeHeader_PasswordIsMasked is the regression test for the
// disk-pass refactor's security guarantee: the plaintext ephemeral password
// must never appear in a rendered home header, regardless of selection state.
func TestRenderHomeHeader_PasswordIsMasked(t *testing.T) {
	const secret = "Kj2x9pQ7mN4vL8rT5wYz1c"
	info := uikit.StartupInfo{
		Port:              9477,
		PasswordEphemeral: true,
		Password:          secret,
	}

	for _, tc := range []struct {
		name   string
		cursor int
	}{
		{name: "no-selection", cursor: -1},
		{name: "password-selected", cursor: 1}, // 0 = Web UI, 1 = Password (no launch ticket)
	} {
		t.Run(tc.name, func(t *testing.T) {
			header, _ := RenderHeader(info, false, 80, tc.cursor, -1)
			assert.NotContains(t, header, secret,
				"plaintext password must never appear in the rendered home header")
			assert.Contains(t, header, "Password", "label should still be present")
			// 22 bullets so the mask matches GeneratePassword's output length.
			assert.Contains(t, header, strings.Repeat("•", PasswordMaskWidth),
				"masked bullets should be rendered in place of the value")
		})
	}
}

func TestRenderHomeHeader_HintWhenPasswordSelected(t *testing.T) {
	info := uikit.StartupInfo{
		Port:              9477,
		PasswordEphemeral: true,
		Password:          "anything",
	}
	header, _ := RenderHeader(info, false, 80, 1, -1)
	assert.Contains(t, header, "press Enter to copy",
		"selected password row should show the copy hint")
}

func TestRenderHomeHeader_OmitsPasswordWhenNotEphemeral(t *testing.T) {
	info := uikit.StartupInfo{
		Port:              9477,
		PasswordEphemeral: false,
		Password:          "should-be-ignored",
	}
	header, _ := RenderHeader(info, false, 80, -1, -1)
	assert.NotContains(t, header, "Password",
		"env-var case must not render a Password field at all")
	assert.NotContains(t, header, "should-be-ignored")
}
