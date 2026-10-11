// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	lipglossv2 "charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
	"github.com/runwisp/runwisp/apps/runwisp/internal/clilog"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// This file maps uikit's palette, the single source of truth for every colour
// RunWisp paints, onto the CLI surfaces that can't reach it on their own.
// Never declare a colour here; pick one from internal/tui/uikit/theme.go.

// brandColorScheme paints fang's help/usage pages in the RunWisp teal. It
// starts from fang's default scheme — which already resolves neutral
// Base/Description/Codeblock colors for both light and dark terminals — and
// overrides only the accents: titles and command names in the primary teal,
// the program name and flags in the logo teal. Fang paints no background but
// does detect it, so the titles get a light/dark pair: the TUI's bright teal
// washes out on a white terminal, where the deeper teal takes over. The logo
// teal reads on both. ErrorHeader is left as fang's default: handleCLIError
// renders errors, not fang, so it would never be seen.
func brandColorScheme(c lipglossv2.LightDarkFunc) fang.ColorScheme {
	cs := fang.DefaultColorScheme(c)
	cs.Title = c(uikit.ColorPrimaryDim, uikit.ColorPrimary)
	cs.Command = cs.Title
	cs.Program = uikit.ColorSecondary
	cs.Flag = uikit.ColorSecondary
	return cs
}

// logColors paints the pretty log handler with the same terminal shades as the
// startup banner, so log lines read on a light or a dark terminal.
var logColors = clilog.LevelColors{
	Dim:   uikit.ColorTextMuted,
	Info:  uikit.ColorSecondary,
	Warn:  uikit.ColorTermWarning,
	Error: uikit.ColorTermDanger,
}

// configureLogs is clilog.Configure with uikit's colours filled in. Every
// caller goes through here so no log setup ends up uncoloured.
func configureLogs(opts clilog.Options) {
	opts.Colors = logColors
	clilog.Configure(opts)
}
