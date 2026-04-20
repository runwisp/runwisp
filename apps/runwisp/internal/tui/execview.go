// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/runwisp/runwisp/internal/model"
)

// maxLogLines caps stored log lines to avoid unbounded memory growth.
const maxLogLines = 100_000

type headerFocusItem int

const (
	headerFocusNone headerFocusItem = iota
	headerFocusBack
	headerFocusAction
	headerFocusStarted
	headerFocusDuration
	headerFocusID
)

type execViewAction int

const (
	execViewActionNone execViewAction = iota
	execViewActionStop
	execViewActionRetry
)

type headerHitBox struct {
	item headerFocusItem
	x0   int
	x1   int
	y    int
}

type execHeaderLayout struct {
	hitBoxes []headerHitBox
}

func (l *execHeaderLayout) reset() {
	l.hitBoxes = l.hitBoxes[:0]
}

func (l *execHeaderLayout) add(item headerFocusItem, x0, x1, y int) {
	l.hitBoxes = append(l.hitBoxes, headerHitBox{
		item: item,
		x0:   x0,
		x1:   x1,
		y:    y,
	})
}

func (l *execHeaderLayout) hitAt(x, y int) headerFocusItem {
	for _, hitBox := range l.hitBoxes {
		if hitBox.y == y && x >= hitBox.x0 && x < hitBox.x1 {
			return hitBox.item
		}
	}
	return headerFocusNone
}

type ExecView struct {
	run           *model.Run
	pane          LogPane
	focused       bool
	headerFocus   headerFocusItem
	hoveredHeader headerFocusItem
	headerLayout  execHeaderLayout
}

func NewExecView(run *model.Run) ExecView {
	return ExecView{
		run: run,
		pane: NewLogPane(LogPaneConfig{
			MaxLines:    maxLogLines,
			LineNumbers: true,
			HScroll:     true,
		}),
	}
}

func (v *ExecView) SetSize(w, h int) {
	v.pane.SetSize(w, h)
	v.pane.SetHeaderHeight(4)
}

func (v *ExecView) SetFocused(focused bool)  { v.focused = focused }
func (v *ExecView) AppendChunk(chunk string) { v.pane.AppendChunk(chunk) }
func (v *ExecView) FlushPending()            { v.pane.FlushPending() }
func (v *ExecView) maxHScroll() int          { return v.pane.MaxHScroll() }
func (v *ExecView) hitAt(x, y int) headerFocusItem {
	return v.headerLayout.hitAt(x, y)
}

func (v *ExecView) RunID() string {
	if v.run == nil {
		return ""
	}
	return v.run.ID
}

func (v *ExecView) Update(msg tea.Msg) tea.Cmd {
	if !v.focused {
		return nil
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	key := keyMsg.String()

	switch key {
	case "up", "k":
		switch v.headerFocus {
		case headerFocusBack, headerFocusID, headerFocusAction:
			return nil
		case headerFocusStarted, headerFocusDuration:
			v.headerFocus = headerFocusID
			return nil
		default:
			if v.pane.scroll > 0 {
				v.pane.scroll--
				v.pane.follow = false
			} else {
				v.headerFocus = headerFocusStarted
			}
			return nil
		}
	case "down", "j":
		switch v.headerFocus {
		case headerFocusBack, headerFocusID, headerFocusAction:
			v.headerFocus = headerFocusStarted
			return nil
		case headerFocusStarted, headerFocusDuration:
			v.headerFocus = headerFocusNone
			return nil
		default:
			v.pane.HandleKeyScroll(key)
			return nil
		}
	case "left":
		switch v.headerFocus {
		case headerFocusAction:
			v.headerFocus = headerFocusID
			return nil
		case headerFocusID:
			v.headerFocus = headerFocusBack
			return nil
		case headerFocusDuration:
			v.headerFocus = headerFocusStarted
			return nil
		case headerFocusBack, headerFocusStarted:
			return nil
		default:
			v.pane.HandleKeyScroll(key)
			return nil
		}
	case "right":
		switch v.headerFocus {
		case headerFocusBack:
			v.headerFocus = headerFocusID
			return nil
		case headerFocusID:
			if v.hasActionButton() {
				v.headerFocus = headerFocusAction
			}
			return nil
		case headerFocusStarted:
			v.headerFocus = headerFocusDuration
			return nil
		case headerFocusAction, headerFocusDuration:
			return nil
		default:
			v.pane.HandleKeyScroll(key)
			return nil
		}
	}

	v.pane.HandleKeyScroll(key)
	return nil
}

func (v *ExecView) Action() execViewAction {
	if v.run == nil {
		return execViewActionNone
	}
	if v.run.Status == model.PhaseRunning {
		return execViewActionStop
	}
	if v.run.IsRetryable() {
		return execViewActionRetry
	}
	return execViewActionNone
}

func (v *ExecView) hasActionButton() bool {
	return v.Action() != execViewActionNone
}

func (v *ExecView) CopyableValue() string {
	return v.copyValueFor(v.headerFocus)
}

func (v *ExecView) copyValueFor(f headerFocusItem) string {
	if v.run == nil {
		return ""
	}
	switch f {
	case headerFocusStarted:
		return v.run.CreatedAt.Local().Format("2006-01-02 15:04:05")
	case headerFocusDuration:
		return formatDuration(*v.run)
	case headerFocusID:
		return v.run.ID
	default:
		return ""
	}
}
