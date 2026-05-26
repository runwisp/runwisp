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

	opts := lineRenderOpts{
		b: b, start: start, end: end, w: w,
		padStyle: padStyle, stdoutStyle: stdoutStyle,
		stderrStyle: stderrStyle, systemStyle: systemStyle,
	}
	if p.Cfg.LineNumbers {
		p.renderLinesWithNumbers(opts)
	} else {
		p.renderLinesPlain(opts)
	}

	rendered := end - start + p.HeaderH
	for rendered < p.Height {
		b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
		b.WriteString("\n")
		rendered++
	}
}

// lineRenderOpts bundles the styling and geometry arguments shared between
// renderLinesWithNumbers and renderLinesPlain.
type lineRenderOpts struct {
	b           *strings.Builder
	start, end  int
	w           int
	padStyle    lipgloss.Style
	stdoutStyle lipgloss.Style
	stderrStyle lipgloss.Style
	systemStyle lipgloss.Style
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

func (p *Pane) renderLinesWithNumbers(o lineRenderOpts) {
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

	highlightBg := lipgloss.NewStyle().
		Background(uikit.ColorWarning).
		Foreground(uikit.ColorBg).
		Width(lnw + 1).
		Bold(true)

	leftIndicatorStyle := lipgloss.NewStyle().
		Background(uikit.ColorBg).
		Foreground(uikit.ColorTextMuted)

	rightIndicatorStyle := lipgloss.NewStyle().
		Foreground(uikit.ColorTextMuted).
		Background(uikit.ColorBg)

	for i := o.start; i < o.end; i++ {
		absLineNum := p.absoluteLineNumber(i)
		isHL := p.HighlightLine != 0 && int64(absLineNum) == p.HighlightLine

		gutterStyle := lineNumStyle
		if isHL {
			gutterStyle = highlightBg
		}
		lineNum := gutterStyle.Render(fmt.Sprintf("%*d ", lnw, absLineNum))

		row := p.Lines[i]
		sliced, clippedRight := uikit.SliceLineColumns(row.Text, p.HScroll, textAreaWidth)
		sliced = trimTrailingRuneIfClipped(sliced, clippedRight)

		textStyle := styleForStream(row.Stream, o.stdoutStyle, o.stderrStyle, o.systemStyle)
		padStyle := o.padStyle
		if isHL {
			textStyle = lipgloss.NewStyle().Background(uikit.ColorWarning).Foreground(uikit.ColorBg).Bold(true)
			padStyle = lipgloss.NewStyle().Background(uikit.ColorWarning)
		}
		lineContent := composeLineContent(sliced, hasLeftIndicator, clippedRight, leftIndicatorStyle, rightIndicatorStyle, textStyle)
		lineContent = padLineContent(lineContent, logContentWidth, padStyle)

		o.b.WriteString(lineNum + lineContent)
		o.b.WriteString("\n")
	}
}

// trimTrailingRuneIfClipped drops one rune off the end of sliced so the "▸"
// indicator can fit in the text-area width. No-op when not clipped or when
// the slice is already empty.
func trimTrailingRuneIfClipped(sliced string, clippedRight bool) string {
	if !clippedRight {
		return sliced
	}
	runes := []rune(sliced)
	if len(runes) == 0 {
		return sliced
	}
	return string(runes[:len(runes)-1])
}

func (p *Pane) renderLinesPlain(o lineRenderOpts) {
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

	for i := o.start; i < o.end; i++ {
		row := p.Lines[i]
		sliced, clippedRight := p.sliceRowText(row.Text, textAreaWidth)

		textStyle := styleForStream(row.Stream, o.stdoutStyle, o.stderrStyle, o.systemStyle)
		lineContent := composeLineContent(sliced, hasLeftIndicator, clippedRight, leftIndicatorStyle, rightIndicatorStyle, textStyle)
		lineContent = padLineContent(lineContent, logContentWidth, o.padStyle)

		o.b.WriteString(o.padStyle.Render("  ") + lineContent)
		o.b.WriteString("\n")
	}
}

// sliceRowText applies horizontal scrolling to a single log row when HScroll
// is enabled. When the slice is clipped on the right we drop one trailing
// rune so the trailing "▸" indicator fits within the text-area width.
func (p *Pane) sliceRowText(text string, textAreaWidth int) (sliced string, clippedRight bool) {
	if !p.Cfg.HScroll {
		return text, false
	}
	sliced, clippedRight = uikit.SliceLineColumns(text, p.HScroll, textAreaWidth)
	return trimTrailingRuneIfClipped(sliced, clippedRight), clippedRight
}

// composeLineContent renders one row's pre-padding content: optional left
// scroll indicator + styled text + optional right scroll indicator.
func composeLineContent(sliced string, hasLeftIndicator, clippedRight bool, leftStyle, rightStyle, textStyle lipgloss.Style) string {
	var lineContent string
	if hasLeftIndicator {
		lineContent = leftStyle.Render("◂") + textStyle.Render(sliced)
	} else {
		lineContent = textStyle.Render(sliced)
	}
	if clippedRight {
		lineContent += rightStyle.Render("▸")
	}
	return lineContent
}

// padLineContent right-pads the rendered content with background-colored
// spaces so every log row fills the available width.
func padLineContent(lineContent string, logContentWidth int, padStyle lipgloss.Style) string {
	visWidth := lipgloss.Width(lineContent)
	if visWidth >= logContentWidth {
		return lineContent
	}
	return lineContent + padStyle.Render(strings.Repeat(" ", logContentWidth-visWidth))
}
