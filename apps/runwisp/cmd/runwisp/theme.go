// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	lipglossv2 "charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// brandColorScheme paints fang's help/usage pages in the RunWisp palette. It
// starts from fang's default scheme — which already resolves neutral
// Base/Description/Codeblock colors for both light and dark terminals — and
// overrides only the accents: titles and command names in brand blue, the
// program name and flags in brand green. ErrorHeader is left as fang's
// default: handleCLIError renders errors, not fang, so it would never be seen.
func brandColorScheme(c lipglossv2.LightDarkFunc) fang.ColorScheme {
	cs := fang.DefaultColorScheme(c)
	cs.Title = uikit.ColorPrimary
	cs.Command = uikit.ColorPrimary
	cs.Program = uikit.ColorSecondary
	cs.Flag = uikit.ColorSecondary
	return cs
}
