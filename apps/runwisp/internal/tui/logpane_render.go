// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// RenderLines renders the visible log lines into the given strings.Builder.
// dimContent controls whether log text uses a dimmed color (e.g. during header focus).
func (p *LogPane) RenderLines(b *strings.Builder, dimContent bool) {
	w := p.width
	visibleLines := p.VisibleLines()

	end := p.scroll + visibleLines
	if end > len(p.lines) {
		end = len(p.lines)
	}
	start := p.scroll
	if start < 0 {
		start = 0
	}

	logFg := colorText
	if dimContent {
		logFg = colorTextDim
	}

	textStyle := lipgloss.NewStyle().
		Background(colorBg).
		Foreground(logFg)

	padStyle := lipgloss.NewStyle().Background(colorBg)

	if p.cfg.LineNumbers {
		p.renderLinesWithNumbers(b, start, end, w, textStyle, padStyle)
	} else {
		p.renderLinesPlain(b, start, end, w, textStyle, padStyle)
	}

	rendered := end - start + p.headerH
	for rendered < p.height {
		b.WriteString(padLine("", w, colorBg))
		b.WriteString("\n")
		rendered++
	}
}

func (p *LogPane) renderLinesWithNumbers(b *strings.Builder, start, end, w int, textStyle, padStyle lipgloss.Style) {
	droppedLines := p.totalLines - len(p.lines)
	lnw := p.lineNumWidth()

	logContentWidth := p.LogContentWidth()

	hasLeftIndicator := p.hScroll > 0
	textAreaWidth := logContentWidth
	if hasLeftIndicator {
		textAreaWidth--
	}

	lineNumStyle := lipgloss.NewStyle().
		Foreground(colorTextMuted).
		Background(colorBg).
		Width(lnw + 1)

	leftIndicatorStyle := lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorTextMuted)

	rightIndicatorStyle := lipgloss.NewStyle().
		Foreground(colorTextMuted).
		Background(colorBg)

	for i := start; i < end; i++ {
		absLineNum := droppedLines + i + 1
		lineNum := lineNumStyle.Render(fmt.Sprintf("%*d ", lnw, absLineNum))

		raw := p.lines[i]
		sliced, clippedRight := sliceLineColumns(raw, p.hScroll, textAreaWidth)

		if clippedRight {
			runes := []rune(sliced)
			if len(runes) > 0 {
				sliced = string(runes[:len(runes)-1])
			}
		}

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

func (p *LogPane) renderLinesPlain(b *strings.Builder, start, end, w int, textStyle, padStyle lipgloss.Style) {
	logContentWidth := p.LogContentWidth()

	hasLeftIndicator := p.cfg.HScroll && p.hScroll > 0
	textAreaWidth := logContentWidth
	if hasLeftIndicator {
		textAreaWidth--
	}

	leftIndicatorStyle := lipgloss.NewStyle().
		Background(colorBg).
		Foreground(colorTextMuted)

	rightIndicatorStyle := lipgloss.NewStyle().
		Foreground(colorTextMuted).
		Background(colorBg)

	for i := start; i < end; i++ {
		raw := p.lines[i]

		var sliced string
		var clippedRight bool
		if p.cfg.HScroll {
			sliced, clippedRight = sliceLineColumns(raw, p.hScroll, textAreaWidth)
			if clippedRight {
				runes := []rune(sliced)
				if len(runes) > 0 {
					sliced = string(runes[:len(runes)-1])
				}
			}
		} else {
			sliced = raw
		}

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

// sliceLineColumns returns a substring of s starting at column offset `start`
// spanning at most `cols` visible columns. Tabs are expanded to 4 spaces.
// The second return value reports whether content extends beyond the visible window.
//
// The function is ANSI-aware: escape sequences are treated as zero-width and
// any colour-setting sequences that appear before the visible window are
// included in the result so that the visible slice renders with the correct
// inherited colour state.
func sliceLineColumns(s string, start, cols int) (string, bool) {
	if cols <= 0 {
		return "", false
	}
	s = strings.ReplaceAll(s, "\t", "    ")

	var (
		buf    strings.Builder
		col    int  // current visual column (printable chars only)
		beyond bool // content extends past start+cols
		done   bool // stop processing further segments
	)

	walkANSISegments(s, func(seg string, printable bool) {
		if done {
			return
		}
		if !printable {
			// Always include ANSI sequences up to the end of the visible window
			// so that colour state set before the window is inherited correctly.
			if !beyond {
				buf.WriteString(seg)
			}
			return
		}
		for _, r := range seg {
			if done {
				break
			}
			w := ansi.StringWidth(string(r))
			if w == 0 {
				// Zero-width characters (combining marks etc.) tag along with
				// the preceding visible character.
				if col > start && col <= start+cols {
					buf.WriteRune(r)
				}
				continue
			}
			if col+w > start+cols {
				beyond = true
				done = true
				break
			}
			if col >= start {
				buf.WriteRune(r)
			}
			col += w
		}
	})

	return buf.String(), beyond
}
