// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logfind

import (
	"fmt"
	"slices"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// View renders the bar as one full-width row, drawn in place of the help bar.
func (m Model) View(width int) string {
	bg := uikit.ColorBgLight
	muted := uikit.OnBg(bg, uikit.ColorTextMuted)
	accent := uikit.OnBg(bg, uikit.ColorPrimary).Bold(true)

	// Every cell is drawn on bg: a bare space would show the terminal's own
	// background through the bar.
	gap := func(n int) string { return uikit.FillBg(n, bg) }

	var b strings.Builder
	b.WriteString(gap(1))
	if m.editing {
		b.WriteString(accent.Render("find ") + m.input.View())
	} else {
		b.WriteString(accent.Render("find ") + uikit.OnBg(bg, uikit.ColorText).Render(fmt.Sprintf("%q", m.input.Value())))
	}
	if status := m.statusText(); status != "" {
		style := uikit.OnBg(bg, uikit.ColorText)
		if m.loading {
			style = muted
		}
		if m.errMsg != "" || (status == "no matches") {
			style = uikit.OnBg(bg, uikit.ColorError)
		}
		b.WriteString(gap(2) + style.Render(status))
	}
	// The options always show, lit when on, like the web console's toggles.
	on := uikit.OnBg(bg, uikit.ColorPrimary).Bold(true)
	faint := uikit.OnBg(bg, uikit.ColorButtonRaisedHover)
	flag := func(label string, set bool) string {
		if set {
			return on.Render(label)
		}
		return faint.Render(label)
	}
	b.WriteString(gap(2) + flag(".*", m.regex) + gap(1) + flag("Aa", m.caseSensitive))
	hints := "n/N next/prev · home/end first/last · pgup/pgdn jump · esc edit"
	if m.editing {
		hints = "enter done · tab regex · alt+c case · esc clear"
	}
	b.WriteString(gap(3) + muted.Render(hints))
	return uikit.PadLine(uikit.TruncateToWidth(b.String(), width), width, bg)
}

// statusText is the match count, or what the search is doing instead.
func (m Model) statusText() string {
	switch {
	case m.errMsg != "":
		return m.errMsg
	case m.input.Value() == "":
		return ""
	case len(m.matches) == 0 && m.loading:
		return "searching…"
	case len(m.matches) == 0:
		return "no matches"
	}
	pos := "–"
	if i, found := slices.BinarySearch(m.matches, m.current); found {
		pos = fmt.Sprint(i + 1)
	}
	plus := ""
	if m.capped {
		plus = "+"
	}
	return fmt.Sprintf("%s of %d%s", pos, len(m.matches), plus)
}
