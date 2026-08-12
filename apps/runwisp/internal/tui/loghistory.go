// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// LogHistoryDialog is a scrollable modal showing the prior whole-region frames a
// settled progress bar / multi-line redraw passed through before committing to
// the anchor line. Frames are shown whole (multi-row redraws as complete K-row
// blocks), oldest first, with the committed line itself labelled at the end.
type LogHistoryDialog struct {
	line   int64     // absolute (0-based) anchor line number, for the title
	rows   []histRow // flattened, pre-rendered display rows
	scroll int
}

type histRow struct {
	text   string
	header bool
}

// NewLogHistoryDialog flattens the frames into display rows. committed is the
// final on-disk text of the anchor line, shown last so the operator sees where
// the animation landed.
func NewLogHistoryDialog(line int64, frames [][]string, committed string) LogHistoryDialog {
	var rows []histRow
	for i, frame := range frames {
		rows = append(rows, histRow{text: frameLabel(i+1, len(frames)), header: true})
		for _, r := range frame {
			rows = append(rows, histRow{text: uikit.SanitizeControls(r)})
		}
	}
	rows = append(rows, histRow{text: "committed", header: true})
	rows = append(rows, histRow{text: uikit.SanitizeControls(committed)})
	return LogHistoryDialog{line: line, rows: rows}
}

func frameLabel(n, total int) string {
	return "Frame " + strconv.Itoa(n) + " of " + strconv.Itoa(total)
}

// logHistoryVisibleRows is how many content rows the modal shows before it
// scrolls internally.
const logHistoryVisibleRows = 16

// Update reports true when the dialog should close.
func (d *LogHistoryDialog) Update(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if _, ok := msg.(tea.MouseClickMsg); ok {
			return true
		}
		return false
	}
	switch keyMsg.String() {
	case "esc", "enter", "q":
		return true
	case "up", "k":
		if d.scroll > 0 {
			d.scroll--
		}
	case "down", "j":
		if d.scroll < d.maxScroll() {
			d.scroll++
		}
	case "pgup":
		d.scroll -= logHistoryVisibleRows
		if d.scroll < 0 {
			d.scroll = 0
		}
	case "pgdown":
		d.scroll += logHistoryVisibleRows
		if d.scroll > d.maxScroll() {
			d.scroll = d.maxScroll()
		}
	case "g", "home":
		d.scroll = 0
	case "G", "end":
		d.scroll = d.maxScroll()
	}
	return false
}

func (d *LogHistoryDialog) maxScroll() int {
	ms := len(d.rows) - logHistoryVisibleRows
	if ms < 0 {
		return 0
	}
	return ms
}

func (d *LogHistoryDialog) View(screenWidth, screenHeight int) string {
	dialogWidth, innerWidth := modalDimensions(screenWidth, 88, 48)

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Frame history — line "+strconv.Itoa(int(d.line)+1), innerWidth, uikit.ColorTextBright, true),
		modalEmptyLine(innerWidth),
	}

	end := d.scroll + logHistoryVisibleRows
	if end > len(d.rows) {
		end = len(d.rows)
	}
	for i := d.scroll; i < end; i++ {
		lines = append(lines, histContentLine(d.rows[i], innerWidth))
	}

	lines = append(lines,
		modalEmptyLine(innerWidth),
		modalSurfaceLine(scrollHint(d.scroll, d.maxScroll()), innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	)

	return renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorSecondary, lines).view
}

func scrollHint(scroll, maxScroll int) string {
	if maxScroll == 0 {
		return "esc close"
	}
	return "↑/↓ scroll · esc close"
}

// histContentLine renders one frame row (or frame header) inside the modal,
// truncated to the inner width. Embedded SGR in committed text is left intact so
// colour survives; the row is clipped by display columns.
func histContentLine(r histRow, innerWidth int) string {
	if r.header {
		return lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorSecondary).
			Bold(true).
			Width(innerWidth).
			Render(r.text)
	}
	sliced, _ := uikit.SliceLineColumns(r.text, 0, innerWidth)
	// Re-assert the modal's own colours after any embedded reset so captured
	// ANSI can't bleed the surface background/foreground.
	sliced = uikit.ReassertResets(sliced, uikit.BaseSGR(uikit.ColorText, uikit.ColorBgLight, false))
	return lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorText).
		Width(innerWidth).
		Render(sliced)
}
