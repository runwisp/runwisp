// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type DebugView struct {
	pane LogPane
}

func NewDebugView() DebugView {
	return DebugView{
		pane: NewLogPane(LogPaneConfig{
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
	v.pane.AppendLine(msg)
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
	w := v.pane.width

	b.WriteString(padLine("", w, colorBgLight))
	b.WriteString("\n")

	title := lipgloss.NewStyle().
		Background(colorBgLight).
		Foreground(colorTextBright).
		Bold(true).
		Render("  Debug Log")
	b.WriteString(padLine(title, w, colorBgLight))
	b.WriteString("\n")

	subtitle := lipgloss.NewStyle().
		Background(colorBgLight).
		Foreground(colorTextMuted).
		Render("  Internal events and diagnostics")
	followIndicator := ""
	if v.pane.follow {
		followIndicator = lipgloss.NewStyle().
			Background(colorBgLight).
			Foreground(colorSecondary).
			Bold(true).
			Render("  ● FOLLOW")
	}
	b.WriteString(padLine(subtitle+followIndicator, w, colorBgLight))
	b.WriteString("\n")

	b.WriteString(padLine("", w, colorBgLight))
	b.WriteString("\n")

	if len(v.pane.lines) == 0 {
		emptyMsg := lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorTextMuted).
			PaddingLeft(2).
			Render("Waiting for events...")
		b.WriteString(padLine(emptyMsg, w, colorBg))
		b.WriteString("\n")
	}

	v.pane.RenderLines(&b, false)

	return b.String()
}
