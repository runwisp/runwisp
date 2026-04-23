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

type LogPaneConfig struct {
	MaxLines    int
	LineNumbers bool
	HScroll     bool
	EndPadding  int // empty lines shown after the last log line; 0 → default (2), capped at VisibleLines/2
}

type LogPane struct {
	cfg         LogPaneConfig
	lines       []string
	pendingLine string
	totalLines  int
	scroll      int
	hScroll     int
	follow      bool
	width       int
	height      int
	headerH     int
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
		p.scroll -= excess
		if p.scroll < 0 {
			p.scroll = 0
		}
	}
	if p.follow {
		p.scroll = p.maxScroll()
	}
}

func (p *LogPane) AppendLine(s string) {
	p.lines = append(p.lines, s)
	p.totalLines++
	p.evictAndFollow()
}

// AppendChunk processes a raw log chunk from a stream. Chunks may contain
// multiple newline-delimited lines and may end mid-line. Incomplete trailing
// content is buffered and prepended to the next chunk.
func (p *LogPane) AppendChunk(chunk string) {
	if chunk == "" {
		return
	}

	combined := p.pendingLine + chunk
	p.pendingLine = ""

	parts := strings.Split(combined, "\n")

	if !strings.HasSuffix(chunk, "\n") {
		p.pendingLine = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	} else {
		if parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
	}

	for i := range parts {
		parts[i] = strings.TrimRight(parts[i], "\r")
	}

	if len(parts) == 0 {
		return
	}

	p.lines = append(p.lines, parts...)
	p.totalLines += len(parts)
	p.evictAndFollow()
}

func (p *LogPane) FlushPending() {
	if p.pendingLine != "" {
		p.lines = append(p.lines, strings.TrimRight(p.pendingLine, "\r"))
		p.totalLines++
		p.pendingLine = ""
		p.evictAndFollow()
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
		expanded := strings.ReplaceAll(p.lines[i], "\t", "    ")
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

func (p *LogPane) lineNumWidth() int {
	w := len(fmt.Sprintf("%d", p.totalLines))
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
