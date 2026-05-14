// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package logpane is the scrollable log viewer shared by the exec-view and
// debug pages. It owns vertical/horizontal scroll, follow-mode, ANSI-aware
// rendering, and line-number gutters.
package logpane

import (
	"fmt"
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
}

func NewPane(cfg Config) Pane {
	return Pane{
		Cfg:    cfg,
		Follow: true,
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
	ms := len(p.Lines) - p.VisibleLines() + p.effectiveEndPadding()
	if ms < 0 {
		return 0
	}
	return ms
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
	}
	if p.Follow {
		p.Scroll = p.maxScroll()
	}
}

// AppendLine appends one absolute-numbered line. The line number is used to
// track totalLines (the highest n+1 seen) so the gutter can size itself for
// the largest expected number even before all lines have arrived.
func (p *Pane) AppendLine(n int64, stream, text string) {
	if len(p.Lines) == 0 && p.FirstLoadedLine == 0 && n > 0 {
		p.FirstLoadedLine = int(n)
	}
	p.Lines = append(p.Lines, Line{Stream: stream, Text: text})
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
// Returns true if the key was consumed.
func (p *Pane) HandleKeyScroll(key string) bool {
	ms := p.maxScroll()

	switch key {
	case "up", "k":
		if p.Scroll > 0 {
			p.Scroll--
			p.Follow = false
			return true
		}
		return false
	case "down", "j":
		if p.Scroll < ms {
			p.Scroll++
		}
		if p.Scroll >= ms {
			p.Follow = true
		}
		return true
	case "pgup":
		p.Scroll -= p.VisibleLines()
		if p.Scroll < 0 {
			p.Scroll = 0
		}
		p.Follow = false
		return true
	case "pgdown":
		p.Scroll += p.VisibleLines()
		if p.Scroll > ms {
			p.Scroll = ms
		}
		if p.Scroll >= ms {
			p.Follow = true
		}
		return true
	case "g", "home":
		p.Scroll = 0
		p.HScroll = 0
		p.Follow = false
		return true
	case "G", "end":
		p.Scroll = ms
		p.Follow = true
		return true
	}

	if p.Cfg.HScroll {
		switch key {
		case "left":
			if p.HScroll > 0 {
				p.HScroll -= HScrollStep
				if p.HScroll < 0 {
					p.HScroll = 0
				}
			}
			return true
		case "right":
			maxH := p.MaxHScroll()
			if p.HScroll < maxH {
				p.HScroll += HScrollStep
				if p.HScroll > maxH {
					p.HScroll = maxH
				}
			}
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
	}

	return false
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
