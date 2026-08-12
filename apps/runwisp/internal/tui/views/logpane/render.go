// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logpane

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// RenderLines renders the visible log lines into the given strings.Builder.
// dimContent controls whether log text uses a dimmed color (e.g. during header
// focus). When loadingOlder is true, a "Loading older logs…" indicator is
// shown at the top.
func (p *Pane) RenderLines(b *strings.Builder, dimContent, loadingOlder bool) {
	w := p.Width
	visibleLines := p.VisibleLines()

	committed := len(p.Lines)
	overlay := p.overlayLines()
	total := committed + len(overlay)

	end := p.Scroll + visibleLines
	if end > total {
		end = total
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

	// Committed lines occupy [start, committed); render only the part inside
	// the scroll window.
	cEnd := end
	if cEnd > committed {
		cEnd = committed
	}
	opts := lineRenderOpts{
		b: b, start: start, end: cEnd, w: w,
		padStyle: padStyle, stdoutStyle: stdoutStyle,
		stderrStyle: stderrStyle, systemStyle: systemStyle,
		// Base SGR per stream, so an embedded reset in captured output
		// re-inherits the pane colours instead of the terminal default.
		stdoutBase: uikit.BaseSGR(logFg, uikit.ColorBg, false),
		stderrBase: uikit.BaseSGR(uikit.ColorError, uikit.ColorBg, false),
		systemBase: uikit.BaseSGR(uikit.ColorTextMuted, uikit.ColorBg, true),
	}
	if start < committed {
		if p.Cfg.LineNumbers {
			p.renderLinesWithNumbers(opts)
		} else {
			p.renderLinesPlain(opts)
		}
	}

	// Live overlay rows occupy [committed, total); render the windowed slice
	// with a blank gutter so they read as an in-place animating tail.
	if end > committed {
		oStart := start - committed
		if oStart < 0 {
			oStart = 0
		}
		p.renderOverlayRows(opts, overlay[oStart:end-committed])
	}

	rendered := end - start + p.HeaderH
	for rendered < p.Height {
		b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
		b.WriteString("\n")
		rendered++
	}
}

// renderOverlayRows renders live-region rows with a blank (numberless) gutter,
// matching the geometry of the committed-line renderers.
func (p *Pane) renderOverlayRows(o lineRenderOpts, rows []Line) {
	logContentWidth := p.LogContentWidth()
	var gutter string
	if p.Cfg.LineNumbers {
		gutter = lipgloss.NewStyle().Background(uikit.ColorBg).Width(p.lineNumWidth() + 1).Render("")
	} else {
		gutter = o.padStyle.Render("  ")
	}
	for _, row := range rows {
		sliced, clippedRight := p.sliceRowText(row.Text, logContentWidth)
		textStyle := styleForStream(row.Stream, o.stdoutStyle, o.stderrStyle, o.systemStyle)
		lineContent := composeLineContent(sliced, o.baseForStream(row.Stream), false, clippedRight, o.padStyle, o.padStyle, textStyle)
		lineContent = padLineContent(lineContent, logContentWidth, o.padStyle)
		o.b.WriteString(gutter + lineContent)
		o.b.WriteString("\n")
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
	stdoutBase  string
	stderrBase  string
	systemBase  string
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

// baseForStream returns the base SGR (see uikit.ReassertResets) matching the
// stream's text style.
func (o lineRenderOpts) baseForStream(stream string) string {
	switch stream {
	case "stderr":
		return o.stderrBase
	case "system":
		return o.systemBase
	default:
		return o.stdoutBase
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

	// Anchor lines (a settled progress bar / redraw with rewindable frames) get
	// a ↻ marker; the selected anchor under the cursor is shown in reverse.
	anchorGutterStyle := lipgloss.NewStyle().
		Foreground(uikit.ColorSecondary).
		Background(uikit.ColorBg).
		Width(lnw + 1)

	cursorGutterStyle := lipgloss.NewStyle().
		Background(uikit.ColorSecondary).
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

		row := p.Lines[i]
		isAnchor := row.FrameCount > 0
		isCursor := p.Cursor == i

		marker := " "
		if isAnchor {
			marker = "↻"
		}
		gutterStyle := lineNumStyle
		switch {
		case isHL:
			gutterStyle = highlightBg
		case isCursor && isAnchor:
			gutterStyle = cursorGutterStyle
		case isAnchor:
			gutterStyle = anchorGutterStyle
		}
		lineNum := gutterStyle.Render(fmt.Sprintf("%*d%s", lnw, absLineNum, marker))
		sliced, clippedRight := uikit.SliceLineColumns(row.Text, p.HScroll, textAreaWidth)
		sliced = trimTrailingRuneIfClipped(sliced, clippedRight)

		textStyle := styleForStream(row.Stream, o.stdoutStyle, o.stderrStyle, o.systemStyle)
		base := o.baseForStream(row.Stream)
		padStyle := o.padStyle
		if isHL {
			textStyle = lipgloss.NewStyle().Background(uikit.ColorWarning).Foreground(uikit.ColorBg).Bold(true)
			padStyle = lipgloss.NewStyle().Background(uikit.ColorWarning)
			base = "" // search-highlight rows carry no user ANSI
		}
		lineContent := composeLineContent(sliced, base, hasLeftIndicator, clippedRight, leftIndicatorStyle, rightIndicatorStyle, textStyle)
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
		lineContent := composeLineContent(sliced, o.baseForStream(row.Stream), hasLeftIndicator, clippedRight, leftIndicatorStyle, rightIndicatorStyle, textStyle)
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
// scroll indicator + styled text + optional right scroll indicator. base is the
// text style's opening SGR, re-asserted after any embedded reset in sliced so
// captured ANSI output can't bleed the pane background/foreground.
func composeLineContent(sliced, base string, hasLeftIndicator, clippedRight bool, leftStyle, rightStyle, textStyle lipgloss.Style) string {
	sliced = uikit.ReassertResets(sliced, base)
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
