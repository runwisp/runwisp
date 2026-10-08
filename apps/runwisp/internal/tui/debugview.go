// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/logpane"
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
	keyMsg, ok := msg.(tea.KeyPressMsg)
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

	title := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextBright).
		Bold(true).
		Render("  Debug Log")
	b.WriteString(uikit.PadLine(title, w, uikit.ColorBgLight))
	b.WriteString("\n")

	subtitle := uikit.OnBg(uikit.ColorBgLight, uikit.ColorTextMuted).
		Render("  Internal events and diagnostics")
	followIndicator := ""
	if v.pane.Follow {
		followIndicator = uikit.OnBg(uikit.ColorBgLight, uikit.ColorSecondary).
			Bold(true).
			Render("  ● FOLLOW")
	}
	b.WriteString(uikit.PadLine(subtitle+followIndicator, w, uikit.ColorBgLight))
	b.WriteString("\n")

	b.WriteString(uikit.PadLine("", w, uikit.ColorBgLight))
	b.WriteString("\n")

	if len(v.pane.Lines) == 0 {
		emptyMsg := uikit.OnBg(uikit.ColorBg, uikit.ColorTextMuted).
			PaddingLeft(2).
			Render("Waiting for events...")
		b.WriteString(uikit.PadLine(emptyMsg, w, uikit.ColorBg))
		b.WriteString("\n")
	}

	v.pane.RenderLines(&b, false, false)

	return b.String()
}
