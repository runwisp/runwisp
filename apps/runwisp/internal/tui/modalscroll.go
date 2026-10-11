// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import tea "charm.land/bubbletea/v2"

// modalScroll is the scroll state of a modal whose body can outgrow the
// screen (the key reference, frame history, task inspector). The modal keeps
// its title and footer fixed and shows a window of its body lines.
type modalScroll struct {
	offset int

	// viewport and total are cached by window so key handling can clamp
	// scrolling without re-deriving the layout. viewport is the number of body
	// rows the modal shows.
	viewport int
	total    int
}

// modalChromeRows is the frame every scrolling modal keeps around its body:
// accent bar, blank+title above, blank+hint+blank below.
const modalChromeRows = 6

// modalBodyRows is how many body rows a scrolling modal shows on a screen
// screenHeight tall, leaving a row of margin above and below the box.
func modalBodyRows(screenHeight int) int {
	return screenHeight - modalChromeRows - 2
}

// handleKey scrolls on the arrow, page and home/end keys, reporting whether
// it used the key.
func (s *modalScroll) handleKey(key string) bool {
	switch key {
	case "up", "k":
		s.scrollBy(-1)
	case "down", "j":
		s.scrollBy(1)
	case "pgup":
		s.scrollBy(-s.viewport)
	case "pgdown":
		s.scrollBy(s.viewport)
	case "g", "home":
		s.offset = 0
	case "G", "end":
		s.offset = s.maxScroll()
	default:
		return false
	}
	return true
}

// handleWheel scrolls one row per wheel notch.
func (s *modalScroll) handleWheel(msg tea.MouseWheelMsg) {
	switch msg.Button {
	case tea.MouseWheelUp:
		s.scrollBy(-1)
	case tea.MouseWheelDown:
		s.scrollBy(1)
	}
}

// scrollBy moves the viewport by delta rows, clamped to the scrollable range.
func (s *modalScroll) scrollBy(delta int) {
	s.offset = min(max(s.offset+delta, 0), s.maxScroll())
}

func (s *modalScroll) maxScroll() int {
	return max(s.total-s.viewport, 0)
}

// window returns the body lines a viewport of rows (at least 3) shows at the
// current offset, re-clamping the offset to the body's current length.
func (s *modalScroll) window(body []string, rows int) []string {
	s.viewport = min(max(rows, 3), len(body))
	s.total = len(body)
	s.offset = min(s.offset, s.maxScroll())
	return body[s.offset : s.offset+s.viewport]
}

// hint is the modal's footer: closeHint, prefixed with the scroll keys when
// there is anything to scroll.
func (s *modalScroll) hint(closeHint string) string {
	if s.maxScroll() == 0 {
		return closeHint
	}
	return "↑/↓ scroll · " + closeHint
}
