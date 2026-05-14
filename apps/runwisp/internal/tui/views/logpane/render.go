// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logpane

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// RenderLines renders the visible log lines into the given strings.Builder.
// dimContent controls whether log text uses a dimmed color (e.g. during header
// focus). When loadingOlder is true, a "Loading older logs…" indicator is
// shown at the top.
func (p *Pane) RenderLines(b *strings.Builder, dimContent, loadingOlder bool) {
	w := p.Width
	visibleLines := p.VisibleLines()

	end := p.Scroll + visibleLines
	if end > len(p.Lines) {
		end = len(p.Lines)
	}
	start := p.Scroll
	if start < 0 {
		start = 0
	}

	logFg := uikit.ColorText
	if dimContent {
		logFg = uikit.ColorTextDim
	}

	stdoutStyle := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(logFg)
	stderrStyle := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorError)
	systemStyle := lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorTextMuted).Italic(true)

	padStyle := lipgloss.NewStyle().Background(uikit.ColorBg)

	if loadingOlder && p.Scroll == 0 {
		loadingStyle := lipgloss.NewStyle().
			Background(uikit.ColorBg).
			Foreground(uikit.ColorTextMuted).
			Italic(true)
		b.WriteString(uikit.PadLine(loadingStyle.Render("  Loading older logs…"), w, uikit.ColorBg))
		b.WriteString("\n")
	}

	if p.Cfg.LineNumbers {
		p.renderLinesWithNumbers(b, start, end, w, padStyle, stdoutStyle, stderrStyle, systemStyle)
	} else {
		p.renderLinesPlain(b, start, end, w, padStyle, stdoutStyle, stderrStyle, systemStyle)
	}

	rendered := end - start + p.HeaderH
	for rendered < p.Height {
		b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
		b.WriteString("\n")
		rendered++
	}
}

func styleForStream(stream string, stdout, stderr, system lipgloss.Style) lipgloss.Style {
	switch stream {
	case "stderr":
		return stderr
	case "system":
		return system
	default:
		return stdout
	}
}

func (p *Pane) renderLinesWithNumbers(b *strings.Builder, start, end, w int, padStyle, stdoutStyle, stderrStyle, systemStyle lipgloss.Style) {
	lnw := p.lineNumWidth()

	logContentWidth := p.LogContentWidth()

	hasLeftIndicator := p.HScroll > 0
	textAreaWidth := logContentWidth
	if hasLeftIndicator {
		textAreaWidth--
	}

	lineNumStyle := lipgloss.NewStyle().
		Foreground(uikit.ColorTextMuted).
		Background(uikit.ColorBg).
		Width(lnw + 1)

	leftIndicatorStyle := lipgloss.NewStyle().
		Background(uikit.ColorBg).
		Foreground(uikit.ColorTextMuted)

	rightIndicatorStyle := lipgloss.NewStyle().
		Foreground(uikit.ColorTextMuted).
		Background(uikit.ColorBg)

	for i := start; i < end; i++ {
		absLineNum := p.absoluteLineNumber(i)
		lineNum := lineNumStyle.Render(fmt.Sprintf("%*d ", lnw, absLineNum))

		row := p.Lines[i]
		sliced, clippedRight := uikit.SliceLineColumns(row.Text, p.HScroll, textAreaWidth)

		if clippedRight {
			runes := []rune(sliced)
			if len(runes) > 0 {
				sliced = string(runes[:len(runes)-1])
			}
		}

		textStyle := styleForStream(row.Stream, stdoutStyle, stderrStyle, systemStyle)

		var lineContent string
		if hasLeftIndicator {
			lineContent = leftIndicatorStyle.Render("◂") + textStyle.Render(sliced)
		} else {
			lineContent = textStyle.Render(sliced)
		}
		if clippedRight {
			lineContent += rightIndicatorStyle.Render("▸")
		}

		visWidth := lipgloss.Width(lineContent)
		if visWidth < logContentWidth {
			lineContent += padStyle.Render(strings.Repeat(" ", logContentWidth-visWidth))
		}

		b.WriteString(lineNum + lineContent)
		b.WriteString("\n")
	}
}

func (p *Pane) renderLinesPlain(b *strings.Builder, start, end, w int, padStyle, stdoutStyle, stderrStyle, systemStyle lipgloss.Style) {
	logContentWidth := p.LogContentWidth()

	hasLeftIndicator := p.Cfg.HScroll && p.HScroll > 0
	textAreaWidth := logContentWidth
	if hasLeftIndicator {
		textAreaWidth--
	}

	leftIndicatorStyle := lipgloss.NewStyle().
		Background(uikit.ColorBg).
		Foreground(uikit.ColorTextMuted)

	rightIndicatorStyle := lipgloss.NewStyle().
		Foreground(uikit.ColorTextMuted).
		Background(uikit.ColorBg)

	for i := start; i < end; i++ {
		row := p.Lines[i]

		var sliced string
		var clippedRight bool
		if p.Cfg.HScroll {
			sliced, clippedRight = uikit.SliceLineColumns(row.Text, p.HScroll, textAreaWidth)
			if clippedRight {
				runes := []rune(sliced)
				if len(runes) > 0 {
					sliced = string(runes[:len(runes)-1])
				}
			}
		} else {
			sliced = row.Text
		}

		textStyle := styleForStream(row.Stream, stdoutStyle, stderrStyle, systemStyle)

		var lineContent string
		if hasLeftIndicator {
			lineContent = leftIndicatorStyle.Render("◂") + textStyle.Render(sliced)
		} else {
			lineContent = textStyle.Render(sliced)
		}
		if clippedRight {
			lineContent += rightIndicatorStyle.Render("▸")
		}

		visWidth := lipgloss.Width(lineContent)
		if visWidth < logContentWidth {
			lineContent += padStyle.Render(strings.Repeat(" ", logContentWidth-visWidth))
		}

		b.WriteString(padStyle.Render("  ") + lineContent)
		b.WriteString("\n")
	}
}
