// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package execlist

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/logpane"
)

// MaxLogLines caps stored log lines to avoid unbounded memory growth.
const MaxLogLines = 100_000

// LogTailLines is the initial tail window when opening a run's log stream.
const LogTailLines = 1000

type HeaderFocusItem int

const (
	HeaderFocusNone HeaderFocusItem = iota
	HeaderFocusBack
	HeaderFocusAction
	HeaderFocusStarted
	HeaderFocusDuration
	HeaderFocusID
)

type Action int

const (
	ActionNone Action = iota
	ActionStop
	ActionRetry
	// ActionStopService cancels every instance of a service and marks
	// it operator-stopped (in-memory for the daemon's lifetime).
	ActionStopService
	// ActionRestartService re-spawns a stopped service or restarts a
	// running one.
	ActionRestartService
	// ActionDelete removes a terminal run record (and its log files).
	// Never offered for running, pending, or service runs.
	ActionDelete
)

type headerHitBox struct {
	item HeaderFocusItem
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

func (l *execHeaderLayout) add(item HeaderFocusItem, x0, x1, y int) {
	l.hitBoxes = append(l.hitBoxes, headerHitBox{
		item: item,
		x0:   x0,
		x1:   x1,
		y:    y,
	})
}

func (l *execHeaderLayout) hitAt(x, y int) HeaderFocusItem {
	for _, hitBox := range l.hitBoxes {
		if hitBox.y == y && x >= hitBox.x0 && x < hitBox.x1 {
			return hitBox.item
		}
	}
	return HeaderFocusNone
}

type ExecView struct {
	Run            *model.Run
	Pane           logpane.Pane
	focused        bool
	fullscreen     bool
	HeaderFocus    HeaderFocusItem
	HoveredHeader  HeaderFocusItem
	headerLayout   execHeaderLayout
	TaskIsService  bool
	serviceStopped bool
	LoadingOlder   bool
}

// execHeaderHeight is the number of header lines drawn above the log in normal mode.
const execHeaderHeight = 4

func NewExecView(run *model.Run) ExecView {
	return ExecView{
		Run: run,
		Pane: logpane.NewPane(logpane.Config{
			MaxLines:    MaxLogLines,
			LineNumbers: true,
			HScroll:     true,
		}),
	}
}

func (v *ExecView) SetSize(w, h int) {
	v.Pane.SetSize(w, h)
	if v.fullscreen {
		v.Pane.SetHeaderHeight(0)
	} else {
		v.Pane.SetHeaderHeight(execHeaderHeight)
	}
}

// ToggleFullscreen flips fullscreen mode. In fullscreen, the header is hidden,
// line numbers are suppressed, and header focus/hover are cleared so the pane
// behaves as a plain scrollback buffer suited for native terminal text selection.
func (v *ExecView) ToggleFullscreen() {
	v.fullscreen = !v.fullscreen
	v.Pane.SetLineNumbers(!v.fullscreen)
	if v.fullscreen {
		v.Pane.SetHeaderHeight(0)
		v.HeaderFocus = HeaderFocusNone
		v.HoveredHeader = HeaderFocusNone
		v.headerLayout.reset()
	} else {
		v.Pane.SetHeaderHeight(execHeaderHeight)
	}
}

func (v *ExecView) Fullscreen() bool        { return v.fullscreen }
func (v *ExecView) SetFocused(focused bool) { v.focused = focused }
func (v *ExecView) MaxHScroll() int         { return v.Pane.MaxHScroll() }
func (v *ExecView) HitAt(x, y int) HeaderFocusItem {
	if v.fullscreen {
		return HeaderFocusNone
	}
	return v.headerLayout.hitAt(x, y)
}

func (v *ExecView) RunID() string {
	if v.Run == nil {
		return ""
	}
	return v.Run.ID
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
	if v.fullscreen {
		v.Pane.HandleKeyScroll(key)
		return nil
	}
	switch key {
	case "up", "k":
		return v.handleKeyUp()
	case "down", "j":
		return v.handleKeyDown(key)
	case "left":
		return v.handleKeyLeft(key)
	case "right":
		return v.handleKeyRight(key)
	}
	v.Pane.HandleKeyScroll(key)
	return nil
}

func (v *ExecView) handleKeyUp() tea.Cmd {
	switch v.HeaderFocus {
	case HeaderFocusBack, HeaderFocusID, HeaderFocusAction:
		return nil
	case HeaderFocusStarted, HeaderFocusDuration:
		v.HeaderFocus = HeaderFocusID
		return nil
	default:
		if v.Pane.Scroll > 0 {
			v.Pane.Scroll--
			v.Pane.Follow = false
		} else {
			v.HeaderFocus = HeaderFocusStarted
		}
		return nil
	}
}

func (v *ExecView) handleKeyDown(key string) tea.Cmd {
	switch v.HeaderFocus {
	case HeaderFocusBack, HeaderFocusID, HeaderFocusAction:
		v.HeaderFocus = HeaderFocusStarted
		return nil
	case HeaderFocusStarted, HeaderFocusDuration:
		v.HeaderFocus = HeaderFocusNone
		return nil
	default:
		v.Pane.HandleKeyScroll(key)
		return nil
	}
}

func (v *ExecView) handleKeyLeft(key string) tea.Cmd {
	switch v.HeaderFocus {
	case HeaderFocusAction:
		v.HeaderFocus = HeaderFocusID
		return nil
	case HeaderFocusID:
		v.HeaderFocus = HeaderFocusBack
		return nil
	case HeaderFocusDuration:
		v.HeaderFocus = HeaderFocusStarted
		return nil
	case HeaderFocusBack, HeaderFocusStarted:
		return nil
	default:
		v.Pane.HandleKeyScroll(key)
		return nil
	}
}

func (v *ExecView) handleKeyRight(key string) tea.Cmd {
	switch v.HeaderFocus {
	case HeaderFocusBack:
		v.HeaderFocus = HeaderFocusID
		return nil
	case HeaderFocusID:
		if v.hasActionButton() {
			v.HeaderFocus = HeaderFocusAction
		}
		return nil
	case HeaderFocusStarted:
		v.HeaderFocus = HeaderFocusDuration
		return nil
	case HeaderFocusAction, HeaderFocusDuration:
		return nil
	default:
		v.Pane.HandleKeyScroll(key)
		return nil
	}
}

func (v *ExecView) Action() Action {
	if v.Run == nil {
		return ActionNone
	}
	if v.TaskIsService {
		// Per-instance retry is not a thing — the supervisor owns the lifecycle
		// (manual retry of a single instance is not a valid operation).
		// The button toggles between stop-the-service and restart-the-service
		// based on the operator-stopped flag.
		if v.serviceStopped {
			return ActionRestartService
		}
		return ActionStopService
	}
	if v.Run.Status == model.PhaseRunning {
		return ActionStop
	}
	if v.Run.Status == model.PhasePending {
		return ActionNone
	}
	if v.Run.IsRetryable() {
		return ActionRetry
	}
	return ActionDelete
}

// CanDelete reports whether the run is in a state that allows deletion.
// Used by keybindings and confirm flows that surface delete independently
// of the header action button.
func (v *ExecView) CanDelete() bool {
	if v.Run == nil || v.TaskIsService {
		return false
	}
	return v.Run.Status != model.PhaseRunning && v.Run.Status != model.PhasePending
}

// SetServiceStopped tells the view whether the parent service has been
// operator-stopped. The view uses this to flip the Stop/Restart button.
func (v *ExecView) SetServiceStopped(stopped bool) { v.serviceStopped = stopped }

// ServiceStopped reports the cached operator-stopped state for the parent service.
func (v *ExecView) ServiceStopped() bool { return v.serviceStopped }

func (v *ExecView) hasActionButton() bool {
	return v.Action() != ActionNone
}

func (v *ExecView) CopyableValue() string {
	return v.CopyValueFor(v.HeaderFocus)
}

func (v *ExecView) CopyValueFor(f HeaderFocusItem) string {
	if v.Run == nil {
		return ""
	}
	switch f {
	case HeaderFocusStarted:
		return v.Run.CreatedAt.Local().Format("2006-01-02 15:04:05")
	case HeaderFocusDuration:
		return uikit.FormatDuration(*v.Run)
	case HeaderFocusID:
		return v.Run.ID
	default:
		return ""
	}
}
