// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	lipglossv2 "charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// brandColorScheme paints fang's help/usage pages in the RunWisp teal. It
// starts from fang's default scheme — which already resolves neutral
// Base/Description/Codeblock colors for both light and dark terminals — and
// overrides only the accents: titles and command names in the bright teal,
// the program name and flags in the logo teal. Fang paints no background, so
// each accent is a light/dark pair: the TUI's dark-surface teals are too pale
// on a white terminal, where the deeper web light-theme teals take over.
// ErrorHeader is left as fang's default: handleCLIError renders errors, not
// fang, so it would never be seen.
func brandColorScheme(c lipglossv2.LightDarkFunc) fang.ColorScheme {
	cs := fang.DefaultColorScheme(c)
	cs.Title = c(uikit.ColorPrimaryDim, uikit.ColorPrimary)
	cs.Command = cs.Title
	cs.Program = c(lipglossv2.Color("#0a6068"), uikit.ColorSecondary) // --rw-primary-active (light)
	cs.Flag = cs.Program
	return cs
}
