// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package execlist

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

const (
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
	Scroll     int // first visible virtual row index
	width      int
	height     int
	focused    bool
	hoveredRow int // -1 = no hover
	// instanceCount resolves a task's currently configured instance count so
	// multi-instance services render a 1-based #N suffix. nil ⇒ no suffix.
	instanceCount func(taskName string) int
}

func NewExecList(w *ExecWindow) ExecList {
	return ExecList{window: w, hoveredRow: -1}
}

// SetInstanceCountLookup wires the task-instance-count resolver used to decide
// whether a row's task name gets a #N instance suffix.
func (e *ExecList) SetInstanceCountLookup(fn func(taskName string) int) {
	e.instanceCount = fn
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
	e.Scroll = 0
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
		e.cursor -= e.ViewportHeight()
		if e.cursor < 0 {
			e.cursor = 0
		}
	case "pgdown":
		e.cursor += e.ViewportHeight()
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
	vpH := e.ViewportHeight()
	if visIdx < 0 || visIdx >= vpH {
		e.hoveredRow = -1
		return
	}
	rowIdx := e.Scroll + visIdx
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
	vpH := e.ViewportHeight()
	if visIdx < 0 || visIdx >= vpH {
		return false
	}
	rowIdx := e.Scroll + visIdx
	if rowIdx < 0 || rowIdx >= n {
		return false
	}
	e.cursor = rowIdx
	return true
}

type scrollbarState struct {
	show                 bool
	thumbStart, thumbEnd int
}

// scrollbarRender bundles the scrollbar's computed state with the pre-styled
// glyphs used to draw it. The three values are always consumed together by the
// row renderer, so they ride as one parameter.
type scrollbarRender struct {
	state scrollbarState
	track string
	thumb string
}

func (e *ExecList) computeScrollbar(vpH, n int) scrollbarState {
	if n <= vpH || vpH <= 0 {
		return scrollbarState{}
	}
	maxScroll := n - vpH + 1
	if maxScroll < 1 {
		maxScroll = 1
	}
	thumbSize := vpH * vpH / n
	if thumbSize < 1 {
		thumbSize = 1
	}
	thumbStart := e.Scroll * (vpH - thumbSize) / maxScroll
	if thumbStart < 0 {
		thumbStart = 0
	}
	thumbEnd := thumbStart + thumbSize
	if thumbEnd > vpH {
		thumbEnd = vpH
	}
	return scrollbarState{show: true, thumbStart: thumbStart, thumbEnd: thumbEnd}
}

func (e *ExecList) buildRowText(item *uikit.ExecListItem, rowIdx int, cw colWidths) string {
	bg, fg, bold := uikit.ColorBg, uikit.ColorText, false
	isCursor := e.focused && rowIdx == e.cursor
	isHovered := rowIdx == e.hoveredRow && !isCursor
	if isCursor {
		bg = uikit.ColorPrimaryDim
		fg = uikit.ColorWhite
		bold = true
	} else if isHovered {
		bg = uikit.ColorExecRowHover
	}
	rowStyle := lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(bold)
	if item == nil {
		// task + four single-space separators + the four fixed columns.
		return rowStyle.Render("  " + padCell("loading…", cw.task+4+cw.fixedSum()))
	}
	statusStr := truncCell(item.Run.DisplayStatus(), cw.status)
	statusBadge := uikit.StatusStyle(statusStr).Render(statusStr)
	statPad := cw.status - lipgloss.Width(statusBadge)
	if statPad < 0 {
		statPad = 0
	}
	statusCell := statusBadge + rowStyle.Render(strings.Repeat(" ", statPad))
	count := 1
	if e.instanceCount != nil {
		count = e.instanceCount(item.Run.TaskName)
	}
	taskLabel := instanceLabel(item.Run.TaskName, item.Run.InstanceIndex, count)
	return rowStyle.Render("  "+padCell(taskLabel, cw.task)+" ") +
		statusCell +
		rowStyle.Render(" "+
			padCell(item.TimeAgo, cw.started)+" "+
			padCell(item.Duration, cw.duration)+" "+
			padCell(string(item.Run.TriggeredBy), cw.trigger))
}

func (e *ExecList) renderDataSection(b *strings.Builder, vpH, n, w, contentW int, cw colWidths, sb scrollbarRender) {
	end := e.Scroll + vpH
	if end > n {
		end = n
	}
	visIdx := 0
	for i := e.Scroll; i < end; i++ {
		text := e.buildRowText(e.window.Item(i), i, cw)
		row := uikit.PadLine(text, contentW, uikit.ColorBg)
		if sb.state.show {
			if visIdx >= sb.state.thumbStart && visIdx < sb.state.thumbEnd {
				row += sb.thumb
			} else {
				row += sb.track
			}
		}
		b.WriteString(uikit.PadLine(row, w, uikit.ColorBg))
		b.WriteString("\n")
		visIdx++
	}
	for visIdx < vpH {
		row := uikit.PadLine("", contentW, uikit.ColorBg)
		if sb.state.show {
			row += sb.track
		}
		b.WriteString(uikit.PadLine(row, w, uikit.ColorBg))
		b.WriteString("\n")
		visIdx++
	}
}

func (e *ExecList) View() string {
	var b strings.Builder
	w := e.width
	n := e.totalCount()
	vpH := e.ViewportHeight()

	sbState := e.computeScrollbar(vpH, n)
	contentW := w
	if sbState.show {
		contentW = w - 1
	}
	cw := computeColWidths(contentW)

	sb := scrollbarRender{
		state: sbState,
		track: lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(lipgloss.Color("#3b3d57")).Render("│"),
		thumb: lipgloss.NewStyle().Background(uikit.ColorBg).Foreground(uikit.ColorText).Render("┃"),
	}

	headerText := "  " +
		padCell("TASK", cw.task) + " " +
		padCell("STATUS", cw.status) + " " +
		padCell("STARTED", cw.started) + " " +
		padCell("DURATION", cw.duration) + " " +
		padCell("TRIGGER", cw.trigger)
	b.WriteString(uikit.PadLine(uikit.TableHeaderStyle.Render(headerText), w, uikit.ColorBgLight))
	b.WriteString("\n")

	if n == 0 {
		emptyMsg := lipgloss.NewStyle().
			Background(uikit.ColorBg).
			Foreground(uikit.ColorTextMuted).
			PaddingLeft(2).
			Render("No executions yet. Waiting for tasks to run...")
		b.WriteString(uikit.PadLine(emptyMsg, w, uikit.ColorBg))
		b.WriteString("\n")
	} else {
		e.renderDataSection(&b, vpH, n, w, contentW, cw, sb)
	}

	if n > 0 {
		from := e.Scroll + 1
		to := e.Scroll + vpH
		if to > n {
			to = n
		}
		footerText := fmt.Sprintf("  viewing %d–%d of %d executions", from, to, n)
		b.WriteString(uikit.PadLine(uikit.TableFooterStyle.Render(footerText), w, uikit.ColorBgLight))
		b.WriteString("\n")
	} else {
		b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
		b.WriteString("\n")
	}

	rendered := strings.Count(b.String(), "\n")
	for rendered < e.height {
		b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
		b.WriteString("\n")
		rendered++
	}

	return b.String()
}

func (e *ExecList) totalCount() int {
	return e.window.TotalCount()
}

// TotalCount returns the total number of executions known to the window.
func (e *ExecList) TotalCount() int { return e.totalCount() }

func (e *ExecList) taskColWidth() int {
	return e.taskColWidthFor(e.width)
}

func (e *ExecList) taskColWidthFor(w int) int {
	return computeColWidths(w).task
}

func (e *ExecList) ViewportHeight() int {
	h := e.height - dataRowOffset - footerLines
	if h < 1 {
		return 1
	}
	return h
}

func (e *ExecList) ensureVisible() {
	vpH := e.ViewportHeight()
	if vpH <= 0 {
		return
	}
	n := e.totalCount()
	if e.cursor < e.Scroll {
		e.Scroll = e.cursor
	} else if e.cursor >= e.Scroll+vpH {
		e.Scroll = e.cursor - vpH + 1
	}
	// When at last item, scroll one extra to reveal end-of-list indicator.
	if n > vpH && e.cursor == n-1 {
		e.Scroll = n - vpH + 1
	}
	maxScroll := n - vpH + 1
	if maxScroll < 0 {
		maxScroll = 0
	}
	if e.Scroll > maxScroll {
		e.Scroll = maxScroll
	}
	if e.Scroll < 0 {
		e.Scroll = 0
	}
}

// ScrollBy adjusts the scroll offset by delta lines, clamping to valid bounds.
// The cursor is moved into the visible area if needed.
func (e *ExecList) ScrollBy(delta int) {
	n := e.totalCount()
	vpH := e.ViewportHeight()
	if vpH <= 0 || n == 0 {
		return
	}
	maxScroll := n - vpH + 1
	if maxScroll < 0 {
		maxScroll = 0
	}
	e.Scroll += delta
	if e.Scroll < 0 {
		e.Scroll = 0
	}
	if e.Scroll > maxScroll {
		e.Scroll = maxScroll
	}
	// Keep cursor within visible area.
	if e.cursor < e.Scroll {
		e.cursor = e.Scroll
	} else if e.cursor >= e.Scroll+vpH {
		e.cursor = e.Scroll + vpH - 1
	}
}

// NeedsFetch returns true if the viewport requires more data from the API.
func (e *ExecList) NeedsFetch() bool {
	return e.window.NeedsFetch(e.Scroll, e.ViewportHeight())
}

// padCell truncates or pads a string to exactly the given visible width.
func padCell(s string, w int) string {
	s = truncCell(s, w)
	if vis := lipgloss.Width(s); vis < w {
		return s + strings.Repeat(" ", w-vis)
	}
	return s
}

// truncCell shortens a string to at most w visible cells, appending an ellipsis
// when it has to cut. It never pads, so the caller controls trailing fill (used
// for the status badge, whose padding must take the row background, not the
// badge color).
func truncCell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	runes := []rune(s)
	if len(runes) > w-1 {
		return string(runes[:w-1]) + "…"
	}
	return s
}
