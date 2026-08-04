// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

import (
	"sync"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// windowSize is the number of items loaded into the sliding window.
// 200 items ≈ 60-80 KB of Run structs — low memory, but enough to scroll
// several pages without needing an immediate fetch.
const windowSize = 200

// fetchAheadMargin triggers a fetch when the viewport is within this many rows
// of the window boundary.
const fetchAheadMargin = 30

// ExecWindow is a virtual-scrolling data source for executions.
// It maintains a sliding window of items loaded from the API, backed by a known
// total count. Only the window slice lives in memory at any time.
//
// Real-time SSE events are inserted at position 0 (newest-first), temporarily
// expanding the window. The window is re-anchored on the next fetch.
type ExecWindow struct {
	mu           sync.Mutex
	client       *apiclient.Client
	filterTask   string
	statusFilter string // "" = all; one of statusFilterCycle
	totalCount   int    // server-reported total
	windowStart  int    // offset of items[0] in the full virtual list
	items        []uikit.ExecListItem
	idSet        map[string]struct{} // dedup for SSE upserts
	loading      bool
}

// statusFilterCycle is the run-status filter the list cycles through with `f`.
// It mirrors the web UI's five run-status buckets (Running / Succeeded / Failed
// / Skipped / Stopped); the empty string means "no filter". Each entry is the
// bucket's label — a clean one-word banner token that also keys StatusStyle —
// which statusFilterWire expands to the full multi-status set sent to the
// server, so the buckets match the web UI exactly (not just the literal token).
var statusFilterCycle = []string{"", "running", "success", "failed", "skipped", "stopped"}

// statusFilterWire maps each statusFilterCycle label to the comma-separated set
// of run statuses (phases + end reasons) sent to the server, mirroring the web
// UI's STATUS_BUCKETS (packages/ui .../run-filters.ts). Without this the TUI
// "Failed" filter matched only literal "failed" and silently dropped
// crashed/timeout/log_overflow/start_failed/missed; likewise Running dropped
// pending, Skipped dropped dst_skipped/queue_full, Stopped dropped
// daemon_stopped. The "failed" set must stay in sync with the web UI's
// NEEDS_ATTENTION_STATUSES (FAILURE_END_REASONS + "missed").
var statusFilterWire = map[string]string{
	"":        "",
	"running": "pending,running",
	"success": "success",
	"failed":  "failed,crashed,timeout,log_overflow,start_failed,missed",
	"skipped": "skipped,dst_skipped,queue_full",
	"stopped": "stopped,daemon_stopped",
}

func NewExecWindow(client *apiclient.Client) *ExecWindow {
	return &ExecWindow{
		client: client,
		idSet:  make(map[string]struct{}),
	}
}

func newExecListItem(run model.Run) uikit.ExecListItem {
	return uikit.ExecListItem{
		Run:      run,
		Duration: uikit.FormatDuration(run),
		TimeAgo:  uikit.FormatTimeAgo(run.CreatedAt),
	}
}

func refreshExecListItem(item *uikit.ExecListItem) {
	item.Duration = uikit.FormatDuration(item.Run)
	item.TimeAgo = uikit.FormatTimeAgo(item.Run.CreatedAt)
}

func (w *ExecWindow) TotalCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.totalCount
}

// SetFilter changes the task-name filter and clears the window. The caller must
// trigger a fetch afterward.
func (w *ExecWindow) SetFilter(taskName string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.filterTask == taskName {
		return
	}
	w.filterTask = taskName
	w.clearLocked()
}

func (w *ExecWindow) FilterTask() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.filterTask
}

// CurrentFilter returns the read-side filter describing the rows the list is
// currently showing. Bulk "select all matching" reuses it so a MatchAll
// selector targets exactly the population on screen (same task + status filter).
func (w *ExecWindow) CurrentFilter() model.RunFilter {
	w.mu.Lock()
	defer w.mu.Unlock()
	return model.RunFilter{Status: statusFilterWire[w.statusFilter], TaskName: w.filterTask}
}

// clearLocked resets the loaded window so the next NeedsFetch returns true. The
// caller must hold w.mu.
func (w *ExecWindow) clearLocked() {
	w.items = nil
	w.idSet = make(map[string]struct{})
	w.windowStart = 0
	w.totalCount = 0
}

// CycleStatusFilter advances to the next run-status filter and clears the window.
// The caller must trigger a fetch afterward.
func (w *ExecWindow) CycleStatusFilter() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.statusFilter = next(statusFilterCycle, w.statusFilter)
	w.clearLocked()
}

// HasStatusFilter reports whether a run-status filter is active (i.e. the list
// is showing a subset rather than all runs).
func (w *ExecWindow) HasStatusFilter() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.statusFilter != ""
}

// StatusFilter returns the raw active status filter (one of statusFilterCycle,
// e.g. "running"/"success"/"failed"/"skipped"/"stopped"), or "" when no filter
// is active.
func (w *ExecWindow) StatusFilter() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.statusFilter
}

// next returns the element after cur in cycle, wrapping around. If cur is not in
// cycle, the first element is returned.
func next(cycle []string, cur string) string {
	for i, v := range cycle {
		if v == cur {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

// Item returns the uikit.ExecListItem at virtual index i, or nil if outside the loaded window.
func (w *ExecWindow) Item(i int) *uikit.ExecListItem {
	w.mu.Lock()
	defer w.mu.Unlock()
	local := i - w.windowStart
	if local < 0 || local >= len(w.items) {
		return nil
	}
	return &w.items[local]
}

// WindowRange returns the start offset and length of the currently loaded
// window.
func (w *ExecWindow) WindowRange() (start, length int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.windowStart, len(w.items)
}

// NeedsFetch returns true if the viewport (scroll..scroll+vpH) is close to
// the window boundary and a fetch should be triggered.
func (w *ExecWindow) NeedsFetch(scroll, vpH int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.loading || w.client == nil {
		return false
	}
	if len(w.items) == 0 {
		return true
	}
	viewEnd := scroll + vpH
	windowEnd := w.windowStart + len(w.items)
	// Near the bottom of the window and more items exist.
	if viewEnd >= windowEnd-fetchAheadMargin && windowEnd < w.totalCount {
		return true
	}
	// Near the top of the window and the window doesn't start at 0.
	if scroll <= w.windowStart+fetchAheadMargin && w.windowStart > 0 {
		return true
	}
	return false
}

// FetchAroundCmd returns a tea.Msg-producing function that loads items centered
// on the given scroll position. Safe to call from a tea.Cmd goroutine.
func (w *ExecWindow) FetchAroundCmd(scroll, vpH int) func() ([]uikit.ExecListItem, int, int, error) {
	w.mu.Lock()
	if w.loading {
		w.mu.Unlock()
		return nil
	}
	w.loading = true
	filter := w.filterTask
	statusFilter := statusFilterWire[w.statusFilter]
	w.mu.Unlock()

	return func() ([]uikit.ExecListItem, int, int, error) {
		// Center the window on the current scroll position.
		offset := scroll - windowSize/2
		if offset < 0 {
			offset = 0
		}

		// The list is always newest-first; sorting was removed as a user-facing
		// feature, so the order is fixed here (keeps the API request unchanged).
		params := apiclient.RunsParams{
			Limit:         windowSize,
			Offset:        offset,
			Status:        statusFilter,
			SortField:     "createdAt",
			SortDirection: "desc",
			TaskName:      filter,
		}

		var runs []model.Run
		var total int64
		var err error
		runs, total, err = w.client.ListRuns(params)
		if err != nil {
			w.mu.Lock()
			w.loading = false
			w.mu.Unlock()
			return nil, 0, 0, err
		}

		items := make([]uikit.ExecListItem, len(runs))
		for i, run := range runs {
			items[i] = newExecListItem(run)
		}

		return items, offset, int(total), nil
	}
}

// ApplyFetch sets the window to the result of a successful fetch.
func (w *ExecWindow) ApplyFetch(items []uikit.ExecListItem, offset, total int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.items = items
	w.windowStart = offset
	w.totalCount = total
	w.loading = false
	w.rebuildIDSet()
}

// SetLoading marks the window as no longer loading (e.g. on error).
func (w *ExecWindow) SetLoading(loading bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.loading = loading
}

// UpsertRun handles a real-time SSE event. New runs are prepended.
// Updated runs are patched in-place if within the window.
func (w *ExecWindow) UpsertRun(run model.Run) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Update existing item in window.
	if _, exists := w.idSet[run.ID]; exists {
		for i := range w.items {
			if w.items[i].Run.ID == run.ID {
				w.items[i].Run = run
				refreshExecListItem(&w.items[i])
				return
			}
		}
	}

	// Skip runs that don't match the active filter.
	if w.filterTask != "" && run.TaskName != w.filterTask {
		return
	}

	// A new run only belongs at position 0 in the newest-first view. Under a
	// status filter its correct position is unknown, so leave it to the next
	// fetch to place it.
	if w.statusFilter != "" {
		return
	}

	// New run — prepend when window starts at 0 (i.e. viewing the top).
	if w.windowStart == 0 {
		item := newExecListItem(run)
		w.items = append([]uikit.ExecListItem{item}, w.items...)
		w.idSet[run.ID] = struct{}{}
		// Trim window if it grew beyond capacity.
		if len(w.items) > windowSize+50 {
			w.items = w.items[:windowSize]
			w.rebuildIDSet()
		}
	}
	w.totalCount++
}

// FindRun returns a copy of the run with the given ID if it exists in the window.
func (w *ExecWindow) FindRun(id string) *model.Run {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.idSet[id]; !exists {
		return nil
	}
	for i := range w.items {
		if w.items[i].Run.ID == id {
			run := w.items[i].Run
			return &run
		}
	}
	return nil
}

// LatestRunning returns the first running execution for the given task, if in window.
func (w *ExecWindow) LatestRunning(taskName string) *model.Run {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.items {
		if w.items[i].Run.TaskName == taskName && w.items[i].Run.Status == model.PhaseRunning {
			run := w.items[i].Run
			return &run
		}
	}
	return nil
}

// UpdateVisibleTimes refreshes TimeAgo/Duration for items in the viewport.
func (w *ExecWindow) UpdateVisibleTimes(scroll, vpH int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	end := scroll + vpH
	if end > w.windowStart+len(w.items) {
		end = w.windowStart + len(w.items)
	}
	start := scroll
	if start < w.windowStart {
		start = w.windowStart
	}
	for i := start; i < end; i++ {
		local := i - w.windowStart
		if local < 0 || local >= len(w.items) {
			continue
		}
		w.items[local].TimeAgo = uikit.FormatTimeAgo(w.items[local].Run.CreatedAt)
		if !w.items[local].Run.Status.IsTerminal() {
			w.items[local].Duration = uikit.FormatDuration(w.items[local].Run)
		}
	}
}

func (w *ExecWindow) rebuildIDSet() {
	w.idSet = make(map[string]struct{}, len(w.items))
	for i := range w.items {
		w.idSet[w.items[i].Run.ID] = struct{}{}
	}
}
