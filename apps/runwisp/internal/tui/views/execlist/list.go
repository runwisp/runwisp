// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

const (
	// Lines above data rows: column header only.
	dataRowOffset = 1
	// Footer line showing the viewing range.
	footerLines = 1
	// Checkbox glyphs for the multi-select column. Each is one cell wide, so it
	// fits the row's existing 2-cell indent without shifting any column.
	checkboxChecked = "▣"
	checkboxEmpty   = "▢"
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
	// selected holds the run IDs explicitly toggled with space. selectAll
	// overrides it: when set, the selection is "every run matching the active
	// filter", rendered all-checked and acted on with a MatchAll selector. The
	// two are mutually exclusive — toggling a row drops selectAll.
	selected  map[string]struct{}
	selectAll bool
}

func NewExecList(w *ExecWindow) ExecList {
	return ExecList{window: w, hoveredRow: -1, selected: make(map[string]struct{})}
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

// SetFilter constrains the list to a specific task name via the window. The
// multi-select is dropped: switching task context changes the row population, so
// a carried-over selection would be meaningless (and confusingly invisible).
func (e *ExecList) SetFilter(taskName string) {
	e.window.SetFilter(taskName)
	e.cursor = 0
	e.Scroll = 0
	e.ClearSelection()
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

// ToggleSelectCursor flips the multi-select state of the run under the cursor.
// It drops select-all first, so the selection becomes an explicit set seeded
// with this row. A no-op when the cursor row isn't loaded.
func (e *ExecList) ToggleSelectCursor() {
	run := e.SelectedRun()
	if run == nil {
		return
	}
	e.selectAll = false
	if _, ok := e.selected[run.ID]; ok {
		delete(e.selected, run.ID)
	} else {
		e.selected[run.ID] = struct{}{}
	}
}

// SelectAllMatching marks every run matching the active filter as selected. The
// explicit set is cleared since select-all subsumes it.
func (e *ExecList) SelectAllMatching() {
	e.selectAll = true
	e.selected = make(map[string]struct{})
}

// ClearSelection drops the entire selection (both modes).
func (e *ExecList) ClearSelection() {
	e.selectAll = false
	e.selected = make(map[string]struct{})
}

// SelectionActive reports whether anything is selected.
func (e *ExecList) SelectionActive() bool {
	return e.selectAll || len(e.selected) > 0
}

// SelectionCount returns how many runs are selected: the full matching total
// under select-all, otherwise the size of the explicit set.
func (e *ExecList) SelectionCount() int {
	if e.selectAll {
		return e.totalCount()
	}
	return len(e.selected)
}

// isRowSelected reports whether the given run ID renders as checked.
func (e *ExecList) isRowSelected(runID string) bool {
	if e.selectAll {
		return true
	}
	_, ok := e.selected[runID]
	return ok
}

// SelectionSelector builds the RunSelector for the current selection and reports
// whether it targets anything. Select-all becomes a MatchAll selector carrying
// the list's active filter (so it respects an outcome filter); an explicit
// selection becomes a sorted IDs selector (sorted for deterministic requests).
func (e *ExecList) SelectionSelector() (model.RunSelector, bool) {
	if e.selectAll {
		return model.RunSelector{MatchAll: true, Filter: e.window.CurrentFilter()}, true
	}
	if len(e.selected) == 0 {
		return model.RunSelector{}, false
	}
	ids := make([]string, 0, len(e.selected))
	for id := range e.selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return model.RunSelector{IDs: ids}, true
}

func (e *ExecList) Update(msg tea.Msg) tea.Cmd {
	if !e.focused {
		return nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	// The filter key works regardless of the current row count so the user can
	// recover from a filter that matched nothing. Cycling it clears the window;
	// the caller refetches via NeedsFetch.
	if keyMsg.String() == "f" {
		e.window.CycleStatusFilter()
		e.cursor, e.Scroll = 0, 0
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
	visIdx := localY - e.headerOffset()
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
func (e *ExecList) HandleClick(localY int) bool {
	n := e.totalCount()
	if n == 0 {
		return false
	}
	visIdx := localY - e.headerOffset()
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

// rowPrefix returns the 2-cell leading indent for a data row. With no selection
// it is the plain indent; while selecting it carries the checkbox glyph plus a
// space — still exactly 2 cells, so no column shifts when selection turns on.
func (e *ExecList) rowPrefix(runID string) string {
	if !e.SelectionActive() {
		return "  "
	}
	if e.isRowSelected(runID) {
		return checkboxChecked + " "
	}
	return checkboxEmpty + " "
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
	statPad := cw.status - uikit.VisibleWidth(statusBadge)
	if statPad < 0 {
		statPad = 0
	}
	statusCell := statusBadge + rowStyle.Render(strings.Repeat(" ", statPad))
	count := 1
	if e.instanceCount != nil {
		count = e.instanceCount(item.Run.TaskName)
	}
	taskLabel := instanceLabel(item.Run.TaskName, item.Run.InstanceIndex, count)
	return rowStyle.Render(e.rowPrefix(item.Run.ID)+padCell(taskLabel, cw.task)+" ") +
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
	// contentW is w when no scrollbar shows and w-1 when it does; the scrollbar
	// glyph is exactly one cell. So a row padded to contentW plus the scrollbar
	// is already exactly w cells — no second PadLine pass needed. Skipping it
	// avoids re-walking a row whose leading scrollbar glyph would otherwise
	// force VisibleWidth onto its slow grapheme path every frame.
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
		b.WriteString(row)
		b.WriteString("\n")
		visIdx++
	}
	for visIdx < vpH {
		row := uikit.PadLine("", contentW, uikit.ColorBg)
		if sb.state.show {
			row += sb.track
		}
		b.WriteString(row)
		b.WriteString("\n")
		visIdx++
	}
}

// renderFilterBanner draws the active-status-filter notice shown under the
// column header. It is tinted with the filter's own status colour so it ties
// visually to the row status badges and can't be missed.
func (e *ExecList) renderFilterBanner(w int) string {
	status := e.window.StatusFilter()
	accent := uikit.StatusStyle(status).GetBackground()
	label := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(accent).
		Bold(true).
		Render("▌ FILTER: " + strings.ToUpper(status))
	hint := lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(uikit.ColorTextMuted).
		Render("  —  press f to change")
	return uikit.PadLine(label+hint, w, uikit.ColorBgLight)
}

// renderEmptySection fills the whole viewport when no rows are present: the
// message on the first line, blank background rows beneath. Filling to vpH keeps
// the footer pinned to the bottom, matching a populated list.
func (e *ExecList) renderEmptySection(b *strings.Builder, vpH, w int) {
	msg := "No executions yet. Waiting for tasks to run..."
	if e.window.HasStatusFilter() {
		msg = "No runs match this filter."
	}
	emptyMsg := lipgloss.NewStyle().
		Background(uikit.ColorBg).
		Foreground(uikit.ColorTextMuted).
		PaddingLeft(2).
		Render(msg)
	b.WriteString(uikit.PadLine(emptyMsg, w, uikit.ColorBg))
	b.WriteString("\n")
	for i := 1; i < vpH; i++ {
		b.WriteString(uikit.PadLine("", w, uikit.ColorBg))
		b.WriteString("\n")
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

	if e.window.HasStatusFilter() {
		b.WriteString(e.renderFilterBanner(w))
		b.WriteString("\n")
	}

	if n == 0 {
		e.renderEmptySection(&b, vpH, w)
	} else {
		e.renderDataSection(&b, vpH, n, w, contentW, cw, sb)
	}

	var footerText string
	switch {
	case e.SelectionActive():
		footerText = fmt.Sprintf("  %d selected", e.SelectionCount())
	case n > 0:
		from := e.Scroll + 1
		to := e.Scroll + vpH
		if to > n {
			to = n
		}
		footerText = fmt.Sprintf("  viewing %d–%d of %d", from, to, n)
	}
	b.WriteString(uikit.PadLine(uikit.TableFooterStyle.Render(footerText), w, uikit.ColorBgLight))
	b.WriteString("\n")

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

// bannerLines is the number of rows the active-filter banner occupies: one
// while a status filter is active, zero otherwise.
func (e *ExecList) bannerLines() int {
	if e.window.HasStatusFilter() {
		return 1
	}
	return 0
}

// headerOffset is the number of rows above the first data row: the column header
// plus the filter banner when it is shown.
func (e *ExecList) headerOffset() int {
	return dataRowOffset + e.bannerLines()
}

func (e *ExecList) ViewportHeight() int {
	h := e.height - e.headerOffset() - footerLines
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
	if vis := uikit.VisibleWidth(s); vis < w {
		return s + strings.Repeat(" ", w-vis)
	}
	return s
}

// truncCell shortens a string to at most w visible cells, appending an ellipsis
// when it has to cut. It never pads, so the caller controls trailing fill (used
// for the status badge, whose padding must take the row background, not the
// badge color).
func truncCell(s string, w int) string {
	return uikit.TruncateToWidth(s, w)
}
