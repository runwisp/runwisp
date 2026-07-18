// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	lipglossv2 "charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
)

// Brand palette — the single source of truth for CLI accent colors, matching
// internal/tui/uikit/theme.go (blue primary, green secondary) and the run-status
// red used across the TUI, banner, and logs. Kept as hex strings so both the
// fang color scheme (lipgloss v2) and the error renderer (lipgloss v1) can draw
// from the same constants.
const (
	brandPrimaryHex   = "#526ee3" // blue
	brandSecondaryHex = "#009371" // green
	brandErrorHex     = "#f7768e" // red
	brandErrorFgHex   = "#1a1b26" // near-black, for text on the red badge
)

// brandColorScheme paints fang's help/usage pages in the RunWisp palette. It
// starts from fang's default scheme — which already resolves neutral
// Base/Description/Codeblock colors for both light and dark terminals — and
// overrides only the accents: titles and command names in brand blue, the
// program name and flags in brand green. ErrorHeader is intentionally left as
// fang's default: fang no longer renders errors (handleCLIError owns that), so
// it would never be seen.
func brandColorScheme(c lipglossv2.LightDarkFunc) fang.ColorScheme {
	cs := fang.DefaultColorScheme(c)
	cs.Title = lipglossv2.Color(brandPrimaryHex)
	cs.Command = lipglossv2.Color(brandPrimaryHex)
	cs.Program = lipglossv2.Color(brandSecondaryHex)
	cs.Flag = lipglossv2.Color(brandSecondaryHex)
	return cs
}
