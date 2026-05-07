// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"
)

const (
	hScrollStep    = 8
	hScrollPadding = 6 // extra cols past the longest visible line so the user can confirm they reached the end
)

// paneLine carries one log line plus its stream so renderers can colour by
// origin (stdout/stderr/system) without re-parsing on-disk prefixes.
type paneLine struct {
	stream string
	text   string
}

type LogPaneConfig struct {
	MaxLines    int
	LineNumbers bool
	HScroll     bool
	EndPadding  int // empty lines shown after the last log line; 0 → default (2), capped at VisibleLines/2
}

type LogPane struct {
	cfg             LogPaneConfig
	lines           []paneLine
	totalLines      int
	scroll          int
	hScroll         int
	follow          bool
	width           int
	height          int
	headerH         int
	firstLoadedLine int
}

func NewLogPane(cfg LogPaneConfig) LogPane {
	return LogPane{
		cfg:    cfg,
		follow: true,
	}
}

func (p *LogPane) SetSize(w, h int) {
	p.width = w
	p.height = h
	p.clampScroll()
}

// clampScroll keeps the vertical scroll within [0, maxScroll]; when following,
// it snaps back to the tail so resize/header-toggle doesn't leave a stale offset.
func (p *LogPane) clampScroll() {
	if p.follow {
		p.scroll = p.maxScroll()
		return
	}
	if ms := p.maxScroll(); p.scroll > ms {
		p.scroll = ms
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
}

// SetHeaderHeight tells the pane how many lines the caller's header occupies.
func (p *LogPane) SetHeaderHeight(h int) {
	p.headerH = h
	p.clampScroll()
}

// SetLineNumbers toggles the left gutter with absolute line numbers.
func (p *LogPane) SetLineNumbers(enabled bool) {
	p.cfg.LineNumbers = enabled
}

func (p *LogPane) VisibleLines() int {
	available := p.height - p.headerH
	if available < 1 {
		return 1
	}
	return available
}

func (p *LogPane) effectiveEndPadding() int {
	pad := p.cfg.EndPadding
	if pad <= 0 {
		pad = 7
	}
	if maxPad := p.VisibleLines() / 2; pad > maxPad {
		pad = maxPad
	}
	return pad
}

func (p *LogPane) maxScroll() int {
	ms := len(p.lines) - p.VisibleLines() + p.effectiveEndPadding()
	if ms < 0 {
		return 0
	}
	return ms
}

func (p *LogPane) evictAndFollow() {
	if p.cfg.MaxLines > 0 && len(p.lines) > p.cfg.MaxLines {
		excess := len(p.lines) - p.cfg.MaxLines
		p.lines = p.lines[excess:]
		p.firstLoadedLine += excess
		p.scroll -= excess
		if p.scroll < 0 {
			p.scroll = 0
		}
	}
	if p.follow {
		p.scroll = p.maxScroll()
	}
}

// AppendLine appends one absolute-numbered line. The line number is used to
// track totalLines (the highest n+1 seen) so the gutter can size itself for
// the largest expected number even before all lines have arrived.
func (p *LogPane) AppendLine(n int64, stream, text string) {
	if len(p.lines) == 0 && p.firstLoadedLine == 0 && n > 0 {
		p.firstLoadedLine = int(n)
	}
	p.lines = append(p.lines, paneLine{stream: stream, text: text})
	if int(n)+1 > p.totalLines {
		p.totalLines = int(n) + 1
	}
	p.evictAndFollow()
}

// EvictBelow drops cached lines whose absolute index is < firstAvailable.
// Used when the streamer reports a server-side rotation.
func (p *LogPane) EvictBelow(firstAvailable int) {
	if firstAvailable <= p.firstLoadedLine {
		return
	}
	skip := firstAvailable - p.firstLoadedLine
	if skip >= len(p.lines) {
		p.lines = p.lines[:0]
	} else {
		p.lines = p.lines[skip:]
	}
	p.firstLoadedLine = firstAvailable
	p.scroll -= skip
	if p.scroll < 0 {
		p.scroll = 0
	}
}

func (p *LogPane) ScrollUp(n int) {
	if p.scroll > 0 {
		p.scroll -= n
		if p.scroll < 0 {
			p.scroll = 0
		}
		p.follow = false
	}
}

func (p *LogPane) ScrollDown(n int) {
	ms := p.maxScroll()
	p.scroll += n
	if p.scroll > ms {
		p.scroll = ms
	}
	if p.scroll >= ms {
		p.follow = true
	}
}

// HandleKeyScroll handles vertical and horizontal scroll keyboard input.
// Returns true if the key was consumed.
func (p *LogPane) HandleKeyScroll(key string) bool {
	ms := p.maxScroll()

	switch key {
	case "up", "k":
		if p.scroll > 0 {
			p.scroll--
			p.follow = false
			return true
		}
		return false
	case "down", "j":
		if p.scroll < ms {
			p.scroll++
		}
		if p.scroll >= ms {
			p.follow = true
		}
		return true
	case "pgup":
		p.scroll -= p.VisibleLines()
		if p.scroll < 0 {
			p.scroll = 0
		}
		p.follow = false
		return true
	case "pgdown":
		p.scroll += p.VisibleLines()
		if p.scroll > ms {
			p.scroll = ms
		}
		if p.scroll >= ms {
			p.follow = true
		}
		return true
	case "g", "home":
		p.scroll = 0
		p.hScroll = 0
		p.follow = false
		return true
	case "G", "end":
		p.scroll = ms
		p.follow = true
		return true
	}

	if p.cfg.HScroll {
		switch key {
		case "left":
			if p.hScroll > 0 {
				p.hScroll -= hScrollStep
				if p.hScroll < 0 {
					p.hScroll = 0
				}
			}
			return true
		case "right":
			maxH := p.MaxHScroll()
			if p.hScroll < maxH {
				p.hScroll += hScrollStep
				if p.hScroll > maxH {
					p.hScroll = maxH
				}
			}
			return true
		case "shift+left":
			p.hScroll = 0
			return true
		case "shift+right":
			maxH := p.MaxHScroll()
			p.hScroll += p.LogContentWidth() / 2
			if p.hScroll > maxH {
				p.hScroll = maxH
			}
			return true
		}
	}

	return false
}

// MaxHScroll returns the maximum useful horizontal scroll offset,
// computed from the widest visible line.
func (p *LogPane) MaxHScroll() int {
	visLines := p.VisibleLines()
	end := p.scroll + visLines
	if end > len(p.lines) {
		end = len(p.lines)
	}
	maxWidth := 0
	for i := p.scroll; i < end; i++ {
		expanded := strings.ReplaceAll(p.lines[i].text, "\t", "    ")
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
func (p *LogPane) PrependLines(lines []paneLine, firstLine int) {
	if len(lines) == 0 {
		return
	}
	p.lines = append(append([]paneLine(nil), lines...), p.lines...)
	p.firstLoadedLine = firstLine
	if p.firstLoadedLine < 0 {
		p.firstLoadedLine = 0
	}
	p.scroll += len(lines)
}

// NeedsOlder reports whether the user has scrolled to the top of the loaded
// buffer and there are older lines on the server that haven't been fetched.
func (p *LogPane) NeedsOlder() bool {
	return p.scroll == 0 && p.firstLoadedLine > 0
}

// SetFirstLoadedLine records the absolute line number of lines[0]. Used after
// a tail / page fetch seeds the buffer at a non-zero anchor.
func (p *LogPane) SetFirstLoadedLine(firstLine int) {
	p.firstLoadedLine = firstLine
}

// FirstLoadedLine returns the absolute line number of lines[0].
func (p *LogPane) FirstLoadedLine() int {
	return p.firstLoadedLine
}

// absoluteLineNumber returns the absolute (1-based) line number for
// the given buffer index.
func (p *LogPane) absoluteLineNumber(bufIdx int) int {
	return p.firstLoadedLine + bufIdx + 1
}

func (p *LogPane) lineNumWidth() int {
	total := p.totalLines
	if total < p.firstLoadedLine+len(p.lines) {
		total = p.firstLoadedLine + len(p.lines)
	}
	w := len(fmt.Sprintf("%d", total))
	if w < 3 {
		w = 3
	}
	return w
}

func (p *LogPane) LogContentWidth() int {
	if !p.cfg.LineNumbers {
		w := p.width - 2
		if w < 10 {
			w = 10
		}
		return w
	}
	w := p.width - p.lineNumWidth() - 1
	if w < 10 {
		w = 10
	}
	return w
}

// SetTotalLines updates the server-known total so the gutter widens for the
// expected maximum even before all lines have arrived.
func (p *LogPane) SetTotalLines(total int) {
	if total > p.totalLines {
		p.totalLines = total
	}
}
