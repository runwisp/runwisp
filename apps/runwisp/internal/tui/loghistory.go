// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
)

// LogHistoryDialog is a scrollable modal showing the prior whole-region frames a
// settled progress bar / multi-line redraw passed through before committing to
// the anchor line. Frames are shown whole (multi-row redraws as complete K-row
// blocks), oldest first, with the committed line itself labelled at the end.
type LogHistoryDialog struct {
	line   int64     // absolute (0-based) anchor line number, for the title
	rows   []histRow // flattened, pre-rendered display rows
	scroll modalScroll
}

type histRow struct {
	text   string
	header bool
}

// NewLogHistoryDialog flattens the frames into display rows. committed is the
// final on-disk text of the anchor line, shown last so the operator sees where
// the animation landed.
func NewLogHistoryDialog(line int64, frames [][]string, committed string) *LogHistoryDialog {
	var rows []histRow
	for i, frame := range frames {
		rows = append(rows, histRow{text: frameLabel(i+1, len(frames)), header: true})
		for _, r := range frame {
			rows = append(rows, histRow{text: uikit.SanitizeControls(r)})
		}
	}
	rows = append(rows, histRow{text: "committed", header: true})
	rows = append(rows, histRow{text: uikit.SanitizeControls(committed)})
	return &LogHistoryDialog{line: line, rows: rows, scroll: modalScroll{viewport: logHistoryVisibleRows, total: len(rows)}}
}

func frameLabel(n, total int) string {
	return "Frame " + strconv.Itoa(n) + " of " + strconv.Itoa(total)
}

// logHistoryVisibleRows is how many content rows the modal shows before it
// scrolls internally.
const logHistoryVisibleRows = 16

// Update reports true when the dialog should close.
func (d *LogHistoryDialog) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "enter", "q":
			return nil, true
		}
		d.scroll.handleKey(msg.String())
	case tea.MouseWheelMsg:
		d.scroll.handleWheel(msg)
	case tea.MouseClickMsg:
		return nil, true
	}
	return nil, false
}

func (d *LogHistoryDialog) View(screenWidth, screenHeight int) string {
	dialogWidth, innerWidth := modalDimensions(screenWidth, 88, 48)

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Frame history — line "+strconv.Itoa(int(d.line)+1), innerWidth, uikit.ColorTextBright, true),
		modalEmptyLine(innerWidth),
	}

	body := make([]string, len(d.rows))
	for i, r := range d.rows {
		body[i] = histContentLine(r, innerWidth)
	}
	lines = append(lines, d.scroll.window(body, logHistoryVisibleRows)...)
	lines = append(lines, modalFooter(d.scroll.hint("esc close"), innerWidth)...)

	return renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorSecondary, lines).view
}

// histContentLine renders one frame row (or frame header) inside the modal,
// truncated to the inner width. Embedded SGR in committed text is left intact so
// colour survives; the row is clipped by display columns.
func histContentLine(r histRow, innerWidth int) string {
	if r.header {
		return modalSectionLine(r.text, innerWidth)
	}
	sliced, _ := uikit.SliceLineColumns(r.text, 0, innerWidth)
	// Re-assert the modal's own colours after any embedded reset so captured
	// ANSI can't bleed the surface background/foreground.
	sliced = uikit.ReassertResets(sliced, uikit.BaseSGR(uikit.ColorText, uikit.ColorBgLight, false))
	return uikit.OnBg(uikit.ColorBgLight, uikit.ColorText).
		Width(innerWidth).
		Render(sliced)
}
