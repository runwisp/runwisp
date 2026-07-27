// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/logpane"
)

type DebugView struct {
	pane    logpane.Pane
	lineSeq int64
}

func NewDebugView() DebugView {
	return DebugView{
		pane: logpane.NewPane(logpane.Config{
			MaxLines:    maxDebugLines,
			LineNumbers: false,
			HScroll:     true,
		}),
	}
}

func (v *DebugView) SetSize(w, h int) {
	v.pane.SetSize(w, h)
	v.pane.SetHeaderHeight(4)
}

func (v *DebugView) AppendLine(msg string) {
	v.pane.AppendLine(v.lineSeq, "stdout", msg)
	v.lineSeq++
}

func (v *DebugView) Update(msg tea.Msg) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return
	}
	v.pane.HandleKeyScroll(keyMsg.String())
}

func (v *DebugView) ScrollUp(n int)   { v.pane.ScrollUp(n) }
func (v *DebugView) ScrollDown(n int) { v.pane.ScrollDown(n) }

func (v *DebugView) View() string {
	var b strings.Builder
	w := v.pane.Width

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")

	title := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextBright).
		Bold(true).
		Render("  Debug Log")
	b.WriteString(uikit.PadLine(title, w, uikit.ColorBgLight))
	b.WriteString("\n")

	subtitle := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted).
		Render("  Internal events and diagnostics")
	followIndicator := ""
	if v.pane.Follow {
		followIndicator = lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorSecondary).
			Bold(true).
			Render("  ● FOLLOW")
	}
	b.WriteString(uikit.PadLine(subtitle+followIndicator, w, uikit.ColorBgLight))
	b.WriteString("\n")

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")

	if len(v.pane.Lines) == 0 {
		emptyMsg := lipgloss.NewStyle().
			Background(uikit.ColorBg).
			Foreground(uikit.ColorTextMuted).
			PaddingLeft(2).
			Render("Waiting for events...")
		b.WriteString(uikit.PadLine(emptyMsg, w, uikit.ColorBg))
		b.WriteString("\n")
	}

	v.pane.RenderLines(&b, false, false)

	return b.String()
}
