// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"sync"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
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
	mu          sync.Mutex
	client      *apiclient.Client
	filterTask  string
	totalCount  int // server-reported total
	windowStart int // offset of items[0] in the full virtual list
	items       []ExecListItem
	idSet       map[string]struct{} // dedup for SSE upserts
	loading     bool
}

func NewExecWindow(client *apiclient.Client) *ExecWindow {
	return &ExecWindow{
		client: client,
		idSet:  make(map[string]struct{}),
	}
}

func newExecListItem(run model.Run) ExecListItem {
	return ExecListItem{
		Run:      run,
		Duration: formatDuration(run),
		TimeAgo:  formatTimeAgo(run.CreatedAt),
	}
}

func refreshExecListItem(item *ExecListItem) {
	item.Duration = formatDuration(item.Run)
	item.TimeAgo = formatTimeAgo(item.Run.CreatedAt)
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
	w.items = nil
	w.idSet = make(map[string]struct{})
	w.windowStart = 0
	w.totalCount = 0
}

func (w *ExecWindow) FilterTask() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.filterTask
}

// Item returns the ExecListItem at virtual index i, or nil if outside the loaded window.
func (w *ExecWindow) Item(i int) *ExecListItem {
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
func (w *ExecWindow) FetchAroundCmd(scroll, vpH int) func() ([]ExecListItem, int, int, error) {
	w.mu.Lock()
	if w.loading {
		w.mu.Unlock()
		return nil
	}
	w.loading = true
	filter := w.filterTask
	w.mu.Unlock()

	return func() ([]ExecListItem, int, int, error) {
		// Center the window on the current scroll position.
		offset := scroll - windowSize/2
		if offset < 0 {
			offset = 0
		}

		params := apiclient.RunsParams{
			Limit:         windowSize,
			Offset:        offset,
			SortField:     "created_at",
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

		items := make([]ExecListItem, len(runs))
		for i, run := range runs {
			items[i] = newExecListItem(run)
		}

		return items, offset, int(total), nil
	}
}

// ApplyFetch sets the window to the result of a successful fetch.
func (w *ExecWindow) ApplyFetch(items []ExecListItem, offset, total int) {
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

	// New run — prepend when window starts at 0 (i.e. viewing the top).
	if w.windowStart == 0 {
		item := newExecListItem(run)
		w.items = append([]ExecListItem{item}, w.items...)
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
		w.items[local].TimeAgo = formatTimeAgo(w.items[local].Run.CreatedAt)
		if !w.items[local].Run.Status.IsTerminal() {
			w.items[local].Duration = formatDuration(w.items[local].Run)
		}
	}
}

func (w *ExecWindow) rebuildIDSet() {
	w.idSet = make(map[string]struct{}, len(w.items))
	for i := range w.items {
		w.idSet[w.items[i].Run.ID] = struct{}{}
	}
}
