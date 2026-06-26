// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package keys is the single source of truth for the TUI's keyboard help text.
//
// The same keybindings are surfaced in three places that used to spell them out
// independently — and drift: the contextual help bar (tui/model_view.go), the
// help overlay (tui/helpdialog.go), and the notifications panel header
// (tui/views/notifications/panel.go). Defining each action once here means the
// three can never again disagree about what a key does.
//
// A Binding carries both renderings an action needs: the spaced Keys/Desc the
// overlay table shows, and the compact Bar segment the width-constrained help
// bar shows. Where the bar wording is conditional (run-now vs restart) or a
// one-off (exec header hints), the bar keeps that logic inline — only the
// shared, repeated segments live here.
package keys

import "strings"

// Binding is one keyboard action. Keys/Desc render the overlay table row; Bar
// is the compact help-bar segment (empty when the action never appears in the
// bar).
type Binding struct {
	Keys string
	Desc string
	Bar  string
}

// Section groups bindings under a heading in the help overlay.
type Section struct {
	Title    string
	Bindings []Binding
}

// Global actions available almost everywhere.
var (
	Help         = Binding{Keys: "?", Desc: "toggle this help", Bar: "? help"}
	Quit         = Binding{Keys: "q / ctrl+c", Desc: "quit", Bar: "q/^C quit"}
	NotifPanel   = Binding{Keys: "n", Desc: "notifications panel (Home)", Bar: "n notifications"}
	ReloadConfig = Binding{Keys: "R", Desc: "reload runwisp.toml", Bar: "R reload"}
	SearchLogs   = Binding{Keys: "/", Desc: "search logs of the focused task"}
	// FilterTasks shares `/` with SearchLogs; it applies when the sidebar is
	// focused, SearchLogs applies in an exec view / on the main panel.
	FilterTasks = Binding{Keys: "/", Desc: "filter tasks (sidebar)", Bar: "/ filter"}
)

// Navigation between and within panels.
var (
	Move        = Binding{Keys: "↑↓ / kj", Desc: "move selection", Bar: "↑↓ navigate"}
	SwitchPanel = Binding{Keys: "←→ / hl", Desc: "switch sidebar ↔ main panel"}
	Open        = Binding{Keys: "enter", Desc: "open / activate / copy field", Bar: "enter open"}
	Back        = Binding{Keys: "esc / ⌫", Desc: "back"}
)

// Task / run actions. RunNow and Restart are the two conditional forms the bar
// picks between; the overlay shows the combined Run row.
var (
	Run      = Binding{Keys: "r", Desc: "run now (task) · restart (service)"}
	RunNow   = Binding{Bar: "r run now"}
	Restart  = Binding{Bar: "r restart"}
	OpenRun  = Binding{Keys: "enter", Desc: "open the selected run"}
	TaskInfo = Binding{Keys: "i", Desc: "inspect — task health, or run details in a log view", Bar: "i details"}
	Undo     = Binding{Keys: "u", Desc: "undo the last action (while the toast shows)", Bar: "u undo"}
)

// Run-list filter action, active when the executions list is focused.
var (
	Filter = Binding{Keys: "f", Desc: "filter by status", Bar: "f filter"}
)

// Run-list multi-select actions. Select/SelectAll start a selection; the rest
// act on it and only appear in the bar once one or more runs are selected.
var (
	Select      = Binding{Keys: "space", Desc: "select / deselect the run under the cursor", Bar: "space select"}
	SelectAll   = Binding{Keys: "a", Desc: "select all runs matching the filter", Bar: "a select all"}
	BulkDelete  = Binding{Keys: "d", Desc: "delete selected runs (undoable)", Bar: "d delete"}
	BulkCancel  = Binding{Keys: "c", Desc: "cancel selected runs", Bar: "c cancel"}
	BulkRerun   = Binding{Keys: "e", Desc: "rerun selected runs", Bar: "e rerun"}
	ClearSelect = Binding{Keys: "esc", Desc: "clear the selection", Bar: "esc clear"}
)

// Exec-view log actions.
var (
	Stop        = Binding{Keys: "s", Desc: "stop run / service"}
	Retry       = Binding{Keys: "r", Desc: "retry · restart"}
	DownloadDel = Binding{Keys: "d / D", Desc: "download log / delete run"}
	Fullscreen  = Binding{Keys: "f", Desc: "fullscreen logs", Bar: "f fullscreen"}
	TopEnd      = Binding{Keys: "g / G", Desc: "jump to top / end"}
	Page        = Binding{Keys: "pgup/pgdn", Desc: "page through logs", Bar: "pgup/pgdn page"}
	FrameHist   = Binding{Keys: "[ / ] · enter", Desc: "prev/next redraw · view frame history"}
	Scroll      = Binding{Bar: "↑↓ scroll"}
	Pan         = Binding{Bar: "←→ pan"}
	BackToList  = Binding{Bar: "esc/⌫ back"}
	ExitFull    = Binding{Bar: "esc/f exit fullscreen"}
	ToSidebar   = Binding{Bar: "esc/← sidebar"}
	BackSidebar = Binding{Bar: "← sidebar"}
	LogJump     = Binding{Bar: "G end  g top  pgup/pgdn page"}
)

// Run Now parameter dialog actions.
var (
	FlagToggle  = Binding{Keys: "space / ←→", Desc: "toggle a flag on/off"}
	ChooseOpt   = Binding{Keys: "←→ / hl", Desc: "choose an option"}
	IncludeOmit = Binding{Keys: "ctrl+t", Desc: "include empty / omit value"}
	RunCancel   = Binding{Keys: "enter", Desc: "run · esc cancel"}
)

// Notifications panel actions. Navigation reuses Move; these add the
// panel-specific actions.
var (
	NotifOpen     = Binding{Keys: "enter", Desc: "open the run", Bar: "enter open"}
	NotifRead     = Binding{Keys: "r", Desc: "mark read", Bar: "r mark read"}
	NotifReadAll  = Binding{Keys: "a", Desc: "mark all read", Bar: "a all read"}
	NotifCollapse = Binding{Keys: "n / esc", Desc: "collapse", Bar: "n/esc collapse"}
)

// OverlaySections is the help overlay's full reference table, in display order.
// Each row's Keys/Desc come from the bindings above, so the overlay and the
// contextual bars are guaranteed consistent.
var OverlaySections = []Section{
	{Title: "Global", Bindings: []Binding{Help, Quit, NotifPanel, ReloadConfig, SearchLogs}},
	{Title: "Navigate", Bindings: []Binding{Move, SwitchPanel, Open, Back, FilterTasks}},
	{Title: "Task", Bindings: []Binding{Run, OpenRun, TaskInfo, Undo}},
	{Title: "Run list", Bindings: []Binding{Filter, Select, SelectAll, BulkDelete, BulkCancel, BulkRerun, ClearSelect}},
	{Title: "Exec view", Bindings: []Binding{Stop, Retry, DownloadDel, Fullscreen, TopEnd, Page, FrameHist}},
	{Title: "Run dialog", Bindings: []Binding{FlagToggle, ChooseOpt, IncludeOmit, RunCancel}},
	{Title: "Notifications", Bindings: []Binding{NotifOpen, NotifRead, NotifReadAll, NotifCollapse}},
}

// JoinBar renders a help-bar line from the given bindings, skipping any without
// a Bar segment, separated by the bar's two-space gap.
func JoinBar(bindings ...Binding) string {
	segs := make([]string, 0, len(bindings))
	for _, b := range bindings {
		if b.Bar != "" {
			segs = append(segs, b.Bar)
		}
	}
	return strings.Join(segs, "  ")
}
