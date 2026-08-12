// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logsearch

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// View renders the overlay centred over (width × height). The caller is
// responsible for compositing the result on top of the existing pane.
func (m *Model) View(width, height int) string {
	w := width - 8
	if w < 40 {
		w = 40
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(uikit.ColorPrimary)
	mutedStyle := lipgloss.NewStyle().Foreground(uikit.ColorTextMuted)
	errStyle := lipgloss.NewStyle().Foreground(uikit.ColorError)

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Search logs for %s", m.taskName)))
	b.WriteString("\n\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	flags := []string{}
	if m.regex {
		flags = append(flags, "regex")
	} else {
		flags = append(flags, "substring")
	}
	if m.caseSensitive {
		flags = append(flags, "case-sensitive")
	}
	b.WriteString(mutedStyle.Render(fmt.Sprintf("(%s) — tab: regex · alt+c: case · enter: search · esc: close", strings.Join(flags, ", "))))
	b.WriteString("\n\n")

	switch {
	case m.loading:
		b.WriteString(mutedStyle.Render("Searching…"))
	case m.errMsg != "":
		b.WriteString(errStyle.Render(m.errMsg))
	case len(m.hits) == 0 && m.input.Value() != "":
		b.WriteString(mutedStyle.Render("No matches."))
	default:
		m.renderHits(&b, w)
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(uikit.ColorPrimary).
		Background(uikit.ColorBg).
		Padding(1, 2).
		Width(w)
	rendered := box.Render(b.String())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}

// renderHits writes the result list to b. Truncates each line to fit width,
// shows a vertical cursor (▶) on the selected row.
func (m *Model) renderHits(b *strings.Builder, width int) {
	hitStyle := lipgloss.NewStyle().Foreground(uikit.ColorText)
	selStyle := lipgloss.NewStyle().Foreground(uikit.ColorBg).Background(uikit.ColorPrimary)
	mutedStyle := lipgloss.NewStyle().Foreground(uikit.ColorTextMuted)

	total := len(m.hits)
	start, end := hitWindow(m.cursor, total)
	for i := start; i < end; i++ {
		h := m.hits[i]
		runShort := h.RunID
		if len(runShort) > 6 {
			runShort = runShort[len(runShort)-6:]
		}
		line := fmt.Sprintf("%s  L%-6d  %s", runShort, h.N, h.Text)
		if w := width - 6; len(line) > w {
			line = line[:w-1] + "…"
		}
		prefix := "  "
		style := hitStyle
		if i == m.cursor {
			prefix = "▶ "
			style = selStyle
		}
		b.WriteString(prefix + style.Render(line))
		b.WriteString("\n")
	}
	if total > MaxVisibleHits {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("hits %d–%d of %d", start+1, end, total)))
	}
}

// hitWindow returns the [start, end) slice of hit indices to render so the
// window of at most MaxVisibleHits rows always contains the cursor. Once the
// cursor passes the bottom of the window the view scrolls to follow it.
func hitWindow(cursor, total int) (start, end int) {
	if total <= MaxVisibleHits {
		return 0, total
	}
	start = 0
	if cursor >= MaxVisibleHits {
		start = cursor - MaxVisibleHits + 1
	}
	if start > total-MaxVisibleHits {
		start = total - MaxVisibleHits
	}
	return start, start + MaxVisibleHits
}
