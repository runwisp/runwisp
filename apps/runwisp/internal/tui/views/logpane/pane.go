// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package logpane is the scrollable log viewer shared by the exec-view and
// debug pages. It owns vertical/horizontal scroll, follow-mode, ANSI-aware
// rendering, and line-number gutters.
package logpane

import (
	"fmt"
	"sort"
	"strings"
)

const (
	HScrollStep    = 8
	hScrollPadding = 6 // extra cols past the longest visible line so the user can confirm they reached the end
)

// Line carries one log line plus its stream so renderers can colour by
// origin (stdout/stderr/system) without re-parsing on-disk prefixes.
type Line struct {
	Stream string
	Text   string
	// FrameCount is non-zero on a settled progress-bar / multi-line-redraw
	// anchor line: the number of prior whole-region frames recorded for it. The
	// gutter marks such lines and the operator can open their history.
	FrameCount int
}

// regionFrame is the current live snapshot of one stream's animating region
// (a `\r` progress bar or multi-line ANSI redraw). It is transient — rendered
// in place at the tail, never part of the committed Lines buffer.
type regionFrame struct {
	epoch int
	rows  []string
}

type Config struct {
	MaxLines    int
	LineNumbers bool
	HScroll     bool
	EndPadding  int // empty lines shown after the last log line; 0 → default (2), capped at VisibleLines/2
}

// Pane is a scrollable log buffer with optional line-number gutter, follow
// mode, horizontal scroll, and prepend/evict support for paged history.
//
// Fields are exported so the owning view (ExecView, DebugView, the root
// Model) can read/write scroll state across package boundaries. Pre-1.0
// the wider TUI keeps these accesses; encapsulation can tighten later.
type Pane struct {
	Cfg             Config
	Lines           []Line
	TotalLines      int
	Scroll          int
	HScroll         int
	Follow          bool
	Width           int
	Height          int
	HeaderH         int
	FirstLoadedLine int
	// HighlightLine is the absolute line number to render in the highlight
	// style after a search-result jump. 0 disables highlighting.
	HighlightLine int64
	// Cursor is the buffer index of the anchor-navigation cursor, or -1 when no
	// anchor line is selected. Moved between frame-history anchor lines with the
	// bracket keys; the selected anchor's history opens on enter.
	Cursor int
	// regions holds the live overlay frame per stream (stdout/stderr). Rendered
	// after the committed lines as an in-place animating tail.
	regions map[string]regionFrame
}

func NewPane(cfg Config) Pane {
	return Pane{
		Cfg:    cfg,
		Follow: true,
		Cursor: -1,
	}
}

func (p *Pane) SetSize(w, h int) {
	p.Width = w
	p.Height = h
	p.clampScroll()
}

// clampScroll keeps the vertical scroll within [0, maxScroll]; when following,
// it snaps back to the tail so resize/header-toggle doesn't leave a stale offset.
func (p *Pane) clampScroll() {
	if p.Follow {
		p.Scroll = p.maxScroll()
		return
	}
	if ms := p.maxScroll(); p.Scroll > ms {
		p.Scroll = ms
	}
	if p.Scroll < 0 {
		p.Scroll = 0
	}
}

// SetHeaderHeight tells the pane how many lines the caller's header occupies.
func (p *Pane) SetHeaderHeight(h int) {
	p.HeaderH = h
	p.clampScroll()
}

// SetLineNumbers toggles the left gutter with absolute line numbers.
func (p *Pane) SetLineNumbers(enabled bool) {
	p.Cfg.LineNumbers = enabled
}

func (p *Pane) VisibleLines() int {
	available := p.Height - p.HeaderH
	if available < 1 {
		return 1
	}
	return available
}

func (p *Pane) effectiveEndPadding() int {
	pad := p.Cfg.EndPadding
	if pad <= 0 {
		pad = 7
	}
	if maxPad := p.VisibleLines() / 2; pad > maxPad {
		pad = maxPad
	}
	return pad
}

func (p *Pane) maxScroll() int {
	ms := p.renderableLen() - p.VisibleLines() + p.effectiveEndPadding()
	if ms < 0 {
		return 0
	}
	return ms
}

// SetRegion replaces the live overlay frame for one stream. Empty rows clears
// the overlay for that stream. Frames are full-state snapshots; the epoch lets
// callers reason about region resets even though we always take the latest.
func (p *Pane) SetRegion(stream string, epoch int, rows []string) {
	if len(rows) == 0 {
		if p.regions != nil {
			delete(p.regions, stream)
		}
	} else {
		if p.regions == nil {
			p.regions = make(map[string]regionFrame)
		}
		p.regions[stream] = regionFrame{epoch: epoch, rows: append([]string(nil), rows...)}
	}
	if p.Follow {
		p.Scroll = p.maxScroll()
	}
}

// ClearRegions drops every live overlay frame. Called when the run ends so a
// dropped clear-frame can't leave a stale animating tail behind.
func (p *Pane) ClearRegions() {
	if len(p.regions) == 0 {
		return
	}
	p.regions = nil
	if p.Follow {
		p.Scroll = p.maxScroll()
	}
}

// overlayLines flattens the live overlay frames into render rows, stdout before
// stderr (then any other streams, sorted) for stable ordering.
func (p *Pane) overlayLines() []Line {
	if len(p.regions) == 0 {
		return nil
	}
	var out []Line
	for _, s := range orderedStreams(p.regions) {
		for _, row := range p.regions[s].rows {
			out = append(out, Line{Stream: s, Text: row})
		}
	}
	return out
}

// orderedStreams returns the region stream keys with stdout first, then stderr,
// then any remaining streams sorted alphabetically.
func orderedStreams(regions map[string]regionFrame) []string {
	out := make([]string, 0, len(regions))
	for _, preferred := range []string{"stdout", "stderr"} {
		if _, ok := regions[preferred]; ok {
			out = append(out, preferred)
		}
	}
	rest := make([]string, 0, len(regions))
	for s := range regions {
		if s != "stdout" && s != "stderr" {
			rest = append(rest, s)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// renderableLen is the total number of rows the pane can scroll through:
// committed lines plus the live overlay tail.
func (p *Pane) renderableLen() int {
	return len(p.Lines) + p.overlayCount()
}

func (p *Pane) overlayCount() int {
	n := 0
	for _, r := range p.regions {
		n += len(r.rows)
	}
	return n
}

// MaxScroll exposes maxScroll for tests and external follow-edge checks.
func (p *Pane) MaxScroll() int { return p.maxScroll() }

func (p *Pane) evictAndFollow() {
	if p.Cfg.MaxLines > 0 && len(p.Lines) > p.Cfg.MaxLines {
		excess := len(p.Lines) - p.Cfg.MaxLines
		p.Lines = p.Lines[excess:]
		p.FirstLoadedLine += excess
		p.Scroll -= excess
		if p.Scroll < 0 {
			p.Scroll = 0
		}
		p.shiftCursor(-excess)
	}
	if p.Follow {
		p.Scroll = p.maxScroll()
	}
}

// AppendLine appends one absolute-numbered line. The line number is used to
// track totalLines (the highest n+1 seen) so the gutter can size itself for
// the largest expected number even before all lines have arrived.
func (p *Pane) AppendLine(n int64, stream, text string) {
	p.AppendLogLine(n, stream, text, 0)
}

// AppendLogLine is AppendLine plus the frame-history count for the line, used
// by the run-log path where a settled progress bar / redraw anchor carries
// rewindable prior frames. frameCount 0 behaves exactly like AppendLine.
func (p *Pane) AppendLogLine(n int64, stream, text string, frameCount int) {
	if len(p.Lines) == 0 && p.FirstLoadedLine == 0 && n > 0 {
		p.FirstLoadedLine = int(n)
	}
	p.Lines = append(p.Lines, Line{Stream: stream, Text: text, FrameCount: frameCount})
	if int(n)+1 > p.TotalLines {
		p.TotalLines = int(n) + 1
	}
	p.evictAndFollow()
}

// EvictBelow drops cached lines whose absolute index is < firstAvailable.
// Used when the streamer reports a server-side rotation.
func (p *Pane) EvictBelow(firstAvailable int) {
	if firstAvailable <= p.FirstLoadedLine {
		return
	}
	skip := firstAvailable - p.FirstLoadedLine
	if skip >= len(p.Lines) {
		p.Lines = p.Lines[:0]
	} else {
		p.Lines = p.Lines[skip:]
	}
	p.FirstLoadedLine = firstAvailable
	p.Scroll -= skip
	if p.Scroll < 0 {
		p.Scroll = 0
	}
	p.shiftCursor(-skip)
}

func (p *Pane) ScrollUp(n int) {
	if p.Scroll > 0 {
		p.Scroll -= n
		if p.Scroll < 0 {
			p.Scroll = 0
		}
		p.Follow = false
	}
}

// JumpToLine centres the pane on the given absolute line and stores it as
// the current HighlightLine. Used by search-result deep-links. If the line
// is not in the loaded buffer, only the highlight is recorded; the caller
// should refetch around that anchor and call this again after the fetch
// completes.
func (p *Pane) JumpToLine(absLine int64) {
	p.HighlightLine = absLine
	bufIdx := int(absLine) - p.FirstLoadedLine - 1
	if bufIdx < 0 || bufIdx >= len(p.Lines) {
		return
	}
	target := bufIdx - p.VisibleLines()/2
	if target < 0 {
		target = 0
	}
	ms := p.maxScroll()
	if target > ms {
		target = ms
	}
	p.Scroll = target
	p.Follow = false
}

// ClearHighlight removes any current highlight. Called on the next user
// scroll so the highlight pulse doesn't linger.
func (p *Pane) ClearHighlight() {
	p.HighlightLine = 0
}

// shiftCursor adjusts the anchor cursor's buffer index after lines are added or
// removed at the front, dropping it to -1 if it falls out of the buffer.
func (p *Pane) shiftCursor(delta int) {
	if p.Cursor < 0 {
		return
	}
	p.Cursor += delta
	if p.Cursor < 0 || p.Cursor >= len(p.Lines) {
		p.Cursor = -1
	}
}

// HasAnchors reports whether any loaded line carries frame history.
func (p *Pane) HasAnchors() bool {
	for i := range p.Lines {
		if p.Lines[i].FrameCount > 0 {
			return true
		}
	}
	return false
}

// MoveCursorToAnchor moves the anchor cursor to the next (dir>0) or previous
// (dir<0) frame-history anchor line, scrolls it into view, and returns true if
// one was found. With no current cursor it starts from the visible edge in the
// direction of travel.
func (p *Pane) MoveCursorToAnchor(dir int) bool {
	if len(p.Lines) == 0 || dir == 0 {
		return false
	}
	start := p.Cursor
	if start < 0 {
		// Seed just outside the visible window so the first hit is on-screen.
		if dir > 0 {
			start = p.Scroll - 1
		} else {
			start = p.Scroll + p.VisibleLines()
		}
	}
	for i := start + dir; i >= 0 && i < len(p.Lines); i += dir {
		if p.Lines[i].FrameCount > 0 {
			p.Cursor = i
			p.scrollCursorIntoView()
			return true
		}
	}
	return false
}

// scrollCursorIntoView nudges the vertical scroll so the cursor line is within
// the visible window, leaving follow mode (the operator is browsing history).
func (p *Pane) scrollCursorIntoView() {
	if p.Cursor < 0 {
		return
	}
	vis := p.VisibleLines()
	if p.Cursor < p.Scroll {
		p.Scroll = p.Cursor
		p.Follow = false
	} else if p.Cursor >= p.Scroll+vis {
		p.Scroll = p.Cursor - vis + 1
		p.Follow = false
	}
	if ms := p.maxScroll(); p.Scroll > ms {
		p.Scroll = ms
	}
	if p.Scroll < 0 {
		p.Scroll = 0
	}
}

// CursorAnchor returns the absolute (0-based) line number and frame count of the
// line under the anchor cursor, and whether the cursor is on a real anchor.
func (p *Pane) CursorAnchor() (absLine int64, frameCount int, ok bool) {
	if p.Cursor < 0 || p.Cursor >= len(p.Lines) {
		return 0, 0, false
	}
	line := p.Lines[p.Cursor]
	if line.FrameCount == 0 {
		return 0, 0, false
	}
	return int64(p.FirstLoadedLine + p.Cursor), line.FrameCount, true
}

// ClearCursor drops the anchor cursor.
func (p *Pane) ClearCursor() {
	p.Cursor = -1
}

func (p *Pane) ScrollDown(n int) {
	ms := p.maxScroll()
	p.Scroll += n
	if p.Scroll > ms {
		p.Scroll = ms
	}
	if p.Scroll >= ms {
		p.Follow = true
	}
}

// HandleKeyScroll handles vertical and horizontal scroll keyboard input.
func (p *Pane) HandleKeyScroll(key string) bool {
	if handled, result := p.handleVScrollKey(key); handled {
		return result
	}
	if p.Cfg.HScroll {
		return p.handleHScrollKey(key)
	}
	return false
}

func (p *Pane) handleVScrollKey(key string) (handled, result bool) {
	ms := p.maxScroll()
	switch key {
	case "up", "k":
		if p.Scroll > 0 {
			p.Scroll--
			p.Follow = false
			return true, true
		}
		return true, false
	case "down", "j":
		p.scrollDown(ms)
		return true, true
	case "pgup":
		p.Scroll -= p.VisibleLines()
		if p.Scroll < 0 {
			p.Scroll = 0
		}
		p.Follow = false
		return true, true
	case "pgdown":
		p.scrollPageDown(ms)
		return true, true
	case "g", "home":
		p.Scroll = 0
		p.HScroll = 0
		p.Follow = false
		return true, true
	case "G", "end":
		p.Scroll = ms
		p.Follow = true
		return true, true
	}
	return false, false
}

func (p *Pane) scrollDown(ms int) {
	if p.Scroll < ms {
		p.Scroll++
	}
	if p.Scroll >= ms {
		p.Follow = true
	}
}

func (p *Pane) scrollPageDown(ms int) {
	p.Scroll += p.VisibleLines()
	if p.Scroll > ms {
		p.Scroll = ms
	}
	if p.Scroll >= ms {
		p.Follow = true
	}
}

func (p *Pane) handleHScrollKey(key string) bool {
	switch key {
	case "left":
		p.scrollLeft()
		return true
	case "right":
		p.scrollRight()
		return true
	case "shift+left":
		p.HScroll = 0
		return true
	case "shift+right":
		maxH := p.MaxHScroll()
		p.HScroll += p.LogContentWidth() / 2
		if p.HScroll > maxH {
			p.HScroll = maxH
		}
		return true
	}
	return false
}

func (p *Pane) scrollLeft() {
	if p.HScroll > 0 {
		p.HScroll -= HScrollStep
		if p.HScroll < 0 {
			p.HScroll = 0
		}
	}
}

func (p *Pane) scrollRight() {
	maxH := p.MaxHScroll()
	if p.HScroll < maxH {
		p.HScroll += HScrollStep
		if p.HScroll > maxH {
			p.HScroll = maxH
		}
	}
}

// MaxHScroll returns the maximum useful horizontal scroll offset,
// computed from the widest visible line.
func (p *Pane) MaxHScroll() int {
	visLines := p.VisibleLines()
	end := p.Scroll + visLines
	if end > len(p.Lines) {
		end = len(p.Lines)
	}
	maxWidth := 0
	for i := p.Scroll; i < end; i++ {
		expanded := strings.ReplaceAll(p.Lines[i].Text, "\t", "    ")
		w := len([]rune(expanded))
		if w > maxWidth {
			maxWidth = w
		}
	}
	content := p.LogContentWidth()
	if maxWidth <= content {
		return 0
	}
	return maxWidth - content + hScrollPadding
}

// PrependLines inserts lines before the current buffer, adjusting scroll so
// the user's visual position stays stable. The first line in `lines` is
// expected to be the lowest absolute line number; the slice is contiguous.
func (p *Pane) PrependLines(lines []Line, firstLine int) {
	if len(lines) == 0 {
		return
	}
	p.Lines = append(append([]Line(nil), lines...), p.Lines...)
	p.FirstLoadedLine = firstLine
	if p.FirstLoadedLine < 0 {
		p.FirstLoadedLine = 0
	}
	p.Scroll += len(lines)
	p.shiftCursor(len(lines))
}

// NeedsOlder reports whether the user has scrolled to the top of the loaded
// buffer and there are older lines on the server that haven't been fetched.
func (p *Pane) NeedsOlder() bool {
	return p.Scroll == 0 && p.FirstLoadedLine > 0
}

// SetFirstLoadedLine records the absolute line number of lines[0]. Used after
// a tail / page fetch seeds the buffer at a non-zero anchor.
func (p *Pane) SetFirstLoadedLine(firstLine int) {
	p.FirstLoadedLine = firstLine
}

// FirstLoadedLineNum returns the absolute line number of lines[0].
func (p *Pane) FirstLoadedLineNum() int {
	return p.FirstLoadedLine
}

// absoluteLineNumber returns the absolute (1-based) line number for
// the given buffer index.
func (p *Pane) absoluteLineNumber(bufIdx int) int {
	return p.FirstLoadedLine + bufIdx + 1
}

func (p *Pane) lineNumWidth() int {
	total := p.TotalLines
	if total < p.FirstLoadedLine+len(p.Lines) {
		total = p.FirstLoadedLine + len(p.Lines)
	}
	w := len(fmt.Sprintf("%d", total))
	if w < 3 {
		w = 3
	}
	return w
}

func (p *Pane) LogContentWidth() int {
	if !p.Cfg.LineNumbers {
		w := p.Width - 2
		if w < 10 {
			w = 10
		}
		return w
	}
	w := p.Width - p.lineNumWidth() - 1
	if w < 10 {
		w = 10
	}
	return w
}

// SetTotalLines updates the server-known total so the gutter widens for the
// expected maximum even before all lines have arrived.
func (p *Pane) SetTotalLines(total int) {
	if total > p.TotalLines {
		p.TotalLines = total
	}
}
