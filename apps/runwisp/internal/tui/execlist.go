// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/model"
)

// Column widths for the execution table (excluding the variable-width task column).
const (
	colStatusW   = 12
	colStartedW  = 20
	colDurationW = 12
	colTriggerW  = 15
	// Lines above data rows: column header only.
	dataRowOffset = 1
	// Footer line showing the viewing range.
	footerLines = 1
)

// ExecList displays a scrollable list of recent executions backed by an
// ExecWindow virtual data source.
type ExecList struct {
	window     *ExecWindow
	cursor     int // selected index in the virtual list (0 = newest)
	scroll     int // first visible virtual row index
	width      int
	height     int
	focused    bool
	hoveredRow int // -1 = no hover
}

func NewExecList(w *ExecWindow) ExecList {
	return ExecList{window: w, hoveredRow: -1}
}

// SetSize updates the available dimensions.
func (e *ExecList) SetSize(w, h int) {
	e.width = w
	e.height = h
	e.ensureVisible()
}

// SetFocused marks whether this panel has keyboard focus.
func (e *ExecList) SetFocused(focused bool) {
	e.focused = focused
	if focused && e.cursor < 0 && e.totalCount() > 0 {
		e.cursor = 0
	}
}

// SetFilter constrains the list to a specific task name via the window.
func (e *ExecList) SetFilter(taskName string) {
	e.window.SetFilter(taskName)
	e.cursor = 0
	e.scroll = 0
}

func (e *ExecList) Cursor() int {
	return e.cursor
}

func (e *ExecList) SelectedRun() *model.Run {
	item := e.window.Item(e.cursor)
	if item != nil {
		return &item.Run
	}
	return nil
}

func (e *ExecList) Update(msg tea.Msg) tea.Cmd {
	if !e.focused {
		return nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	n := e.totalCount()
	if n == 0 {
		return nil
	}
	switch keyMsg.String() {
	case "up", "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down", "j":
		if e.cursor < n-1 {
			e.cursor++
		}
	case "pgup":
		e.cursor -= e.viewportHeight()
		if e.cursor < 0 {
			e.cursor = 0
		}
	case "pgdown":
		e.cursor += e.viewportHeight()
		if e.cursor >= n {
			e.cursor = n - 1
		}
	case "home":
		e.cursor = 0
	case "end":
		e.cursor = n - 1
	default:
		return nil
	}
	e.ensureVisible()
	return nil
}

func (e *ExecList) SetHovered(row int) {
	e.hoveredRow = row
}

// SetHoveredFromLocalY computes hover from a Y position relative to the exec list top.
func (e *ExecList) SetHoveredFromLocalY(localY int) {
	n := e.totalCount()
	if n == 0 {
		e.hoveredRow = -1
		return
	}
	visIdx := localY - dataRowOffset
	vpH := e.viewportHeight()
	if visIdx < 0 || visIdx >= vpH {
		e.hoveredRow = -1
		return
	}
	rowIdx := e.scroll + visIdx
	if rowIdx < 0 || rowIdx >= n {
		e.hoveredRow = -1
		return
	}
	e.hoveredRow = rowIdx
}

// HandleClick selects a row based on localY (relative to the ExecList top).
// Returns true if a valid row was selected.
func (e *ExecList) HandleClick(localY int) bool {
	n := e.totalCount()
	if n == 0 {
		return false
	}
	visIdx := localY - dataRowOffset
	vpH := e.viewportHeight()
	if visIdx < 0 || visIdx >= vpH {
		return false
	}
	rowIdx := e.scroll + visIdx
	if rowIdx < 0 || rowIdx >= n {
		return false
	}
	e.cursor = rowIdx
	return true
}

func (e *ExecList) View() string {
	var b strings.Builder
	w := e.width
	n := e.totalCount()
	vpH := e.viewportHeight()

	// Scrollbar state — computed first to determine content width.
	showScrollbar := n > vpH && vpH > 0
	contentW := w
	if showScrollbar {
		contentW = w - 1
	}
	taskW := e.taskColWidthFor(contentW)

	var thumbStart, thumbEnd int
	if showScrollbar {
		maxScroll := n - vpH + 1
		if maxScroll < 1 {
			maxScroll = 1
		}
		thumbSize := vpH * vpH / n
		if thumbSize < 1 {
			thumbSize = 1
		}
		thumbStart = e.scroll * (vpH - thumbSize) / maxScroll
		if thumbStart < 0 {
			thumbStart = 0
		}
		thumbEnd = thumbStart + thumbSize
		if thumbEnd > vpH {
			thumbEnd = vpH
		}
	}

	scrollTrack := lipgloss.NewStyle().Background(colorBg).Foreground(lipgloss.Color("#3b3d57")).Render("│")
	scrollThumb := lipgloss.NewStyle().Background(colorBg).Foreground(colorText).Render("┃")

	// Column headers.
	headerText := "  " +
		padCell("TASK", taskW) + " " +
		padCell("STATUS", colStatusW) + " " +
		padCell("STARTED", colStartedW) + " " +
		padCell("DURATION", colDurationW) + " " +
		padCell("TRIGGER", colTriggerW)
	b.WriteString(padLine(tableHeaderStyle.Render(headerText), w, colorBgLight))
	b.WriteString("\n")

	if n == 0 {
		emptyMsg := lipgloss.NewStyle().
			Background(colorBg).
			Foreground(colorTextMuted).
			PaddingLeft(2).
			Render("No executions yet. Waiting for tasks to run...")
		b.WriteString(padLine(emptyMsg, w, colorBg))
		b.WriteString("\n")
	} else {

		// Data rows.
		end := e.scroll + vpH
		if end > n {
			end = n
		}
		visIdx := 0
		for i := e.scroll; i < end; i++ {
			item := e.window.Item(i)
			bg, fg, bold := colorBg, colorText, false
			isCursor := e.focused && i == e.cursor
			isHovered := i == e.hoveredRow && !isCursor

			if isCursor {
				bg = colorPrimaryDim
				fg = colorWhite
				bold = true
			} else if isHovered {
				bg = colorExecRowHover
			}

			rowStyle := lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(bold)

			var text string
			if item != nil {
				statusStr := item.Run.DisplayStatus()
				statusBadge := statusStyle(statusStr).Render(statusStr)
				badgeW := lipgloss.Width(statusBadge)
				statPad := colStatusW - badgeW
				if statPad < 0 {
					statPad = 0
				}
				statusCell := statusBadge + rowStyle.Render(strings.Repeat(" ", statPad))
				text = rowStyle.Render("  "+padCell(item.Run.TaskName, taskW)+" ") +
					statusCell +
					rowStyle.Render(" "+
						padCell(item.TimeAgo, colStartedW)+" "+
						padCell(item.Duration, colDurationW)+" "+
						padCell(string(item.Run.TriggeredBy), colTriggerW))
			} else {
				// Item not in window — show loading placeholder.
				text = rowStyle.Render("  " + padCell("loading…", taskW+1+colStatusW+1+colStartedW+1+colDurationW+1+colTriggerW))
			}

			row := padLine(text, contentW, colorBg)
			if showScrollbar {
				if visIdx >= thumbStart && visIdx < thumbEnd {
					row += scrollThumb
				} else {
					row += scrollTrack
				}
			}
			b.WriteString(padLine(row, w, colorBg))
			b.WriteString("\n")
			visIdx++
		}

		// Fill remaining viewport rows with scrollbar track.
		for visIdx < vpH {
			row := padLine("", contentW, colorBg)
			if showScrollbar {
				row += scrollTrack
			}
			b.WriteString(padLine(row, w, colorBg))
			b.WriteString("\n")
			visIdx++
		}
	}

	// Footer with viewing range.
	if n > 0 {
		from := e.scroll + 1
		to := e.scroll + vpH
		if to > n {
			to = n
		}
		footerText := fmt.Sprintf("  viewing %d–%d of %d executions", from, to, n)
		b.WriteString(padLine(tableFooterStyle.Render(footerText), w, colorBgLight))
		b.WriteString("\n")
	} else {
		b.WriteString(padLine("", w, colorBg))
		b.WriteString("\n")
	}

	// Fill remaining space below viewport.
	rendered := strings.Count(b.String(), "\n")
	for rendered < e.height {
		b.WriteString(padLine("", w, colorBg))
		b.WriteString("\n")
		rendered++
	}

	return b.String()
}

// --- Internal helpers ---

func (e *ExecList) totalCount() int {
	return e.window.TotalCount()
}

func (e *ExecList) taskColWidth() int {
	return e.taskColWidthFor(e.width)
}

func (e *ExecList) taskColWidthFor(w int) int {
	fixed := 2 + 4 + colStatusW + colStartedW + colDurationW + colTriggerW
	tw := w - fixed
	if tw < 10 {
		return 10
	}
	return tw
}

func (e *ExecList) viewportHeight() int {
	h := e.height - dataRowOffset - footerLines
	if h < 1 {
		return 1
	}
	return h
}

func (e *ExecList) ensureVisible() {
	vpH := e.viewportHeight()
	if vpH <= 0 {
		return
	}
	n := e.totalCount()
	if e.cursor < e.scroll {
		e.scroll = e.cursor
	} else if e.cursor >= e.scroll+vpH {
		e.scroll = e.cursor - vpH + 1
	}
	// When at last item, scroll one extra to reveal end-of-list indicator.
	if n > vpH && e.cursor == n-1 {
		e.scroll = n - vpH + 1
	}
	maxScroll := n - vpH + 1
	if maxScroll < 0 {
		maxScroll = 0
	}
	if e.scroll > maxScroll {
		e.scroll = maxScroll
	}
	if e.scroll < 0 {
		e.scroll = 0
	}
}

// ScrollBy adjusts the scroll offset by delta lines, clamping to valid bounds.
// The cursor is moved into the visible area if needed.
func (e *ExecList) ScrollBy(delta int) {
	n := e.totalCount()
	vpH := e.viewportHeight()
	if vpH <= 0 || n == 0 {
		return
	}
	maxScroll := n - vpH + 1
	if maxScroll < 0 {
		maxScroll = 0
	}
	e.scroll += delta
	if e.scroll < 0 {
		e.scroll = 0
	}
	if e.scroll > maxScroll {
		e.scroll = maxScroll
	}
	// Keep cursor within visible area.
	if e.cursor < e.scroll {
		e.cursor = e.scroll
	} else if e.cursor >= e.scroll+vpH {
		e.cursor = e.scroll + vpH - 1
	}
}

// NeedsFetch returns true if the viewport requires more data from the API.
func (e *ExecList) NeedsFetch() bool {
	return e.window.NeedsFetch(e.scroll, e.viewportHeight())
}

// --- Utilities ---

// padCell truncates or pads a string to exactly the given visible width.
func padCell(s string, w int) string {
	vis := lipgloss.Width(s)
	if vis > w {
		runes := []rune(s)
		if len(runes) > w-1 {
			return string(runes[:w-1]) + "…"
		}
		return s
	}
	if vis < w {
		return s + strings.Repeat(" ", w-vis)
	}
	return s
}

func formatDuration(run model.Run) string {
	if run.StartAt == nil {
		return "—"
	}
	endTime := time.Now()
	if run.EndAt != nil {
		endTime = *run.EndAt
	}
	d := endTime.Sub(*run.StartAt)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hrs := int(d.Hours())
	mins := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hrs, mins)
}

func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("Jan 02 15:04")
	}
}
