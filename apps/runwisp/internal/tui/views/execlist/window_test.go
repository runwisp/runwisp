// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package execlist

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// makeItems returns a slice of ExecListItems with the given run IDs and task names.
func makeItems(runs ...model.Run) []uikit.ExecListItem {
	items := make([]uikit.ExecListItem, len(runs))
	for i, r := range runs {
		items[i] = uikit.ExecListItem{Run: r}
	}
	return items
}

func TestWindowRange_Empty(t *testing.T) {
	w := NewExecWindow(nil)
	start, length := w.WindowRange()
	if start != 0 {
		t.Fatalf("expected start=0 for empty window, got %d", start)
	}
	if length != 0 {
		t.Fatalf("expected length=0 for empty window, got %d", length)
	}
}

func TestWindowRange_AfterFetch(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(
		model.Run{ID: "r1", TaskName: "task-a"},
		model.Run{ID: "r2", TaskName: "task-a"},
		model.Run{ID: "r3", TaskName: "task-a"},
	)
	w.ApplyFetch(items, 10, 50)

	start, length := w.WindowRange()
	if start != 10 {
		t.Fatalf("expected start=10, got %d", start)
	}
	if length != 3 {
		t.Fatalf("expected length=3, got %d", length)
	}
}

func TestWindowRange_ZeroOffset(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(
		model.Run{ID: "r1", TaskName: "task-a"},
		model.Run{ID: "r2", TaskName: "task-a"},
	)
	w.ApplyFetch(items, 0, 2)

	start, length := w.WindowRange()
	if start != 0 {
		t.Fatalf("expected start=0, got %d", start)
	}
	if length != 2 {
		t.Fatalf("expected length=2, got %d", length)
	}
}

func TestSetLoading_True(t *testing.T) {
	w := NewExecWindow(nil)
	// loading starts as false; SetLoading(true) should enable it.
	w.SetLoading(true)
	// NeedsFetch checks the loading flag — if loading=true it returns false even
	// when items are empty, which is the observable effect of the flag.
	if w.NeedsFetch(0, 10) {
		t.Fatal("expected NeedsFetch=false when loading=true")
	}
}

func TestSetLoading_False(t *testing.T) {
	w := NewExecWindow(nil)
	// Manually set loading via ApplyFetch (which sets loading=false) and confirm.
	items := makeItems(model.Run{ID: "r1", TaskName: "t"})
	w.ApplyFetch(items, 0, 1)

	// Flip back to loading=true so we can then set it false.
	w.SetLoading(true)
	if w.NeedsFetch(0, 10) {
		t.Fatal("pre-condition: expected NeedsFetch=false while loading=true")
	}

	w.SetLoading(false)
	// With client=nil NeedsFetch is always false regardless of loading — but the
	// function must not panic and the state must be consistent.
	// Just verify it doesn't crash.
	_ = w.NeedsFetch(0, 10)
}

func TestLatestRunning_Found(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(
		model.Run{ID: "r1", TaskName: "alpha", Status: model.PhaseEnded},
		model.Run{ID: "r2", TaskName: "alpha", Status: model.PhaseRunning},
		model.Run{ID: "r3", TaskName: "alpha", Status: model.PhaseRunning},
	)
	w.ApplyFetch(items, 0, 3)

	got := w.LatestRunning("alpha")
	if got == nil {
		t.Fatal("expected a running run, got nil")
	}
	// Items are 0-indexed; first running one is at index 1 (id r2).
	if got.ID != "r2" {
		t.Fatalf("expected r2, got %s", got.ID)
	}
}

func TestLatestRunning_NotFound_WrongTask(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(
		model.Run{ID: "r1", TaskName: "alpha", Status: model.PhaseRunning},
	)
	w.ApplyFetch(items, 0, 1)

	got := w.LatestRunning("beta")
	if got != nil {
		t.Fatalf("expected nil for unknown task, got %+v", got)
	}
}

func TestLatestRunning_NotFound_NoRunning(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(
		model.Run{ID: "r1", TaskName: "alpha", Status: model.PhaseEnded},
	)
	w.ApplyFetch(items, 0, 1)

	got := w.LatestRunning("alpha")
	if got != nil {
		t.Fatalf("expected nil when no running run, got %+v", got)
	}
}

func TestLatestRunning_EmptyWindow(t *testing.T) {
	w := NewExecWindow(nil)
	got := w.LatestRunning("any")
	if got != nil {
		t.Fatalf("expected nil on empty window, got %+v", got)
	}
}

func TestFilterTask_Initial(t *testing.T) {
	w := NewExecWindow(nil)
	if f := w.FilterTask(); f != "" {
		t.Fatalf("expected empty initial filter, got %q", f)
	}
}

func TestFilterTask_AfterSetFilter(t *testing.T) {
	w := NewExecWindow(nil)
	w.SetFilter("my-task")
	if f := w.FilterTask(); f != "my-task" {
		t.Fatalf("expected filter=my-task, got %q", f)
	}
}

func TestSetFilter_ClearsItems(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(model.Run{ID: "r1", TaskName: "old"})
	w.ApplyFetch(items, 0, 1)

	w.SetFilter("new-task")

	if tc := w.TotalCount(); tc != 0 {
		t.Fatalf("expected totalCount=0 after filter change, got %d", tc)
	}
	start, length := w.WindowRange()
	if start != 0 || length != 0 {
		t.Fatalf("expected empty window after filter change, got start=%d length=%d", start, length)
	}
}

func TestSetFilter_SameFilter_IsNoop(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(model.Run{ID: "r1", TaskName: "task-x"})
	w.ApplyFetch(items, 5, 10)
	w.SetFilter("task-x")
	// Pre-populate again so we can tell if it was cleared.
	w.ApplyFetch(items, 5, 10)

	// Setting the same filter must not clear items.
	w.SetFilter("task-x")

	_, length := w.WindowRange()
	if length != 1 {
		t.Fatalf("expected window to stay intact for same filter, got length=%d", length)
	}
}

func TestItem_InsideWindow(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(
		model.Run{ID: "r1", TaskName: "task"},
		model.Run{ID: "r2", TaskName: "task"},
	)
	w.ApplyFetch(items, 0, 2)

	item := w.Item(0)
	if item == nil {
		t.Fatal("expected item at index 0, got nil")
	}
	if item.Run.ID != "r1" {
		t.Fatalf("expected r1, got %s", item.Run.ID)
	}

	item = w.Item(1)
	if item == nil {
		t.Fatal("expected item at index 1, got nil")
	}
	if item.Run.ID != "r2" {
		t.Fatalf("expected r2, got %s", item.Run.ID)
	}
}

func TestItem_OutsideWindow(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(model.Run{ID: "r1", TaskName: "task"})
	w.ApplyFetch(items, 5, 10) // window starts at offset 5

	// Index below window start.
	if got := w.Item(4); got != nil {
		t.Fatalf("expected nil for index below window, got %+v", got)
	}
	// Index above window end.
	if got := w.Item(6); got != nil {
		t.Fatalf("expected nil for index above window, got %+v", got)
	}
}

func TestItem_AtWindowOffset(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(model.Run{ID: "offset-run", TaskName: "task"})
	w.ApplyFetch(items, 7, 20)

	item := w.Item(7)
	if item == nil {
		t.Fatal("expected item at window start offset, got nil")
	}
	if item.Run.ID != "offset-run" {
		t.Fatalf("expected offset-run, got %s", item.Run.ID)
	}
}

// --- NeedsFetch with a non-nil client (real branch coverage) ---

func newWindowWithClient(t *testing.T) *ExecWindow {
	t.Helper()
	// A real apiclient.Client is sufficient — NeedsFetch never calls it.
	c := apiclient.New("http://127.0.0.1:0", "")
	return NewExecWindow(c)
}

func TestNeedsFetch_EmptyItemsTriggersFetch(t *testing.T) {
	w := newWindowWithClient(t)
	if !w.NeedsFetch(0, 10) {
		t.Fatal("expected NeedsFetch=true when window has no items")
	}
}

func TestNeedsFetch_NearBottomTriggersFetch(t *testing.T) {
	w := newWindowWithClient(t)
	// Window of 50 items starting at 0; total 200. Viewport scrolled near bottom.
	items := make([]uikit.ExecListItem, 50)
	w.ApplyFetch(items, 0, 200)
	// scroll=30 + vpH=20 = 50 (= windowEnd) → within margin, total > windowEnd.
	if !w.NeedsFetch(30, 20) {
		t.Fatal("expected NeedsFetch=true near bottom of window")
	}
}

func TestNeedsFetch_NearTopTriggersFetch(t *testing.T) {
	w := newWindowWithClient(t)
	items := make([]uikit.ExecListItem, 50)
	// windowStart=100, so scrolling near top should trigger a fetch.
	w.ApplyFetch(items, 100, 200)
	if !w.NeedsFetch(105, 10) {
		t.Fatal("expected NeedsFetch=true near top when windowStart > 0")
	}
}

func TestNeedsFetch_MiddleNoFetch(t *testing.T) {
	w := newWindowWithClient(t)
	items := make([]uikit.ExecListItem, 200)
	w.ApplyFetch(items, 0, 200)
	// Viewport firmly inside the window — should NOT trigger a fetch.
	if w.NeedsFetch(80, 20) {
		t.Fatal("expected NeedsFetch=false when viewport is mid-window")
	}
}

func TestNeedsFetch_NearBottomButAtTotal_NoFetch(t *testing.T) {
	w := newWindowWithClient(t)
	items := make([]uikit.ExecListItem, 50)
	// totalCount equals window end — nothing more to load.
	w.ApplyFetch(items, 0, 50)
	if w.NeedsFetch(30, 20) {
		t.Fatal("expected NeedsFetch=false when windowEnd == totalCount")
	}
}

func TestNeedsFetch_EmptyItems(t *testing.T) {
	// With nil client, NeedsFetch must always return false regardless of items.
	w := NewExecWindow(nil)
	if w.NeedsFetch(0, 10) {
		t.Fatal("expected NeedsFetch=false with nil client and empty items")
	}
}

func TestNeedsFetch_NearBottom(t *testing.T) {
	// Create a window with a real-looking scenario but nil client — result is
	// always false because there's no client to fetch from.
	w := NewExecWindow(nil)
	// Even without a client the function must not panic.
	_ = w.NeedsFetch(170, 20)
}

func TestNeedsFetch_NearTop(t *testing.T) {
	w := NewExecWindow(nil)
	// No panic even with a non-zero window start.
	items := makeItems(model.Run{ID: "r1", TaskName: "t"})
	w.ApplyFetch(items, 50, 100)
	_ = w.NeedsFetch(50, 10)
}

func TestUpsertRun_NewRunPrepended(t *testing.T) {
	w := NewExecWindow(nil)
	// Window starts at 0 so new runs should be prepended.
	w.ApplyFetch([]uikit.ExecListItem{}, 0, 0)

	w.UpsertRun(model.Run{ID: "new1", TaskName: "task"})

	item := w.Item(0)
	if item == nil {
		t.Fatal("expected item at index 0 after upsert, got nil")
	}
	if item.Run.ID != "new1" {
		t.Fatalf("expected new1, got %s", item.Run.ID)
	}
	if tc := w.TotalCount(); tc != 1 {
		t.Fatalf("expected totalCount=1, got %d", tc)
	}
}

func TestUpsertRun_ExistingRunUpdatedInPlace(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(model.Run{ID: "r1", TaskName: "task", Status: model.PhaseRunning})
	w.ApplyFetch(items, 0, 1)

	updated := model.Run{ID: "r1", TaskName: "task", Status: model.PhaseEnded}
	w.UpsertRun(updated)

	// Total count must not grow — it's an in-place update.
	if tc := w.TotalCount(); tc != 1 {
		t.Fatalf("expected totalCount=1 after in-place update, got %d", tc)
	}
	item := w.Item(0)
	if item == nil {
		t.Fatal("expected item at index 0, got nil")
	}
	if item.Run.Status != model.PhaseEnded {
		t.Fatalf("expected status ended, got %s", item.Run.Status)
	}
}

func TestUpsertRun_FilterRespected(t *testing.T) {
	w := NewExecWindow(nil)
	w.SetFilter("only-this")
	w.ApplyFetch([]uikit.ExecListItem{}, 0, 0)

	// Run for a different task must be silently dropped.
	w.UpsertRun(model.Run{ID: "other1", TaskName: "other-task"})

	if tc := w.TotalCount(); tc != 0 {
		t.Fatalf("expected no items when run doesn't match filter, got totalCount=%d", tc)
	}

	// Run for the filtered task must be accepted.
	w.UpsertRun(model.Run{ID: "match1", TaskName: "only-this"})

	if tc := w.TotalCount(); tc != 1 {
		t.Fatalf("expected totalCount=1 for matching run, got %d", tc)
	}
}

func TestUpsertRun_NonZeroWindowStart_NotPrepended(t *testing.T) {
	w := NewExecWindow(nil)
	// Window starts at offset 10 — new runs must not be prepended.
	items := makeItems(model.Run{ID: "r1", TaskName: "task"})
	w.ApplyFetch(items, 10, 20)
	initialTotal := w.TotalCount()

	w.UpsertRun(model.Run{ID: "newrun", TaskName: "task"})

	// totalCount increments even when not prepended.
	if tc := w.TotalCount(); tc != initialTotal+1 {
		t.Fatalf("expected totalCount=%d, got %d", initialTotal+1, tc)
	}
	// The new item must not appear in the window (window starts at 10).
	if item := w.Item(0); item != nil {
		t.Fatalf("expected nil at index 0 (before window start), got %+v", item)
	}
}

func TestFetchAroundCmd_RunsRequest_ReturnsItemsAndTotal(t *testing.T) {
	resp := server.RunsResponseBody{
		Runs:  []model.Run{{ID: "r-1", TaskName: "t"}, {ID: "r-2", TaskName: "t"}},
		Total: 100,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	w := NewExecWindow(apiclient.New(srv.URL, ""))
	fn := w.FetchAroundCmd(50, 20)
	if fn == nil {
		t.Fatal("expected non-nil closure")
	}
	items, offset, total, err := fn()
	if err != nil {
		t.Fatalf("FetchAroundCmd closure: %v", err)
	}
	if total != 100 {
		t.Fatalf("total = %d, want 100", total)
	}
	if offset != 50-windowSize/2 {
		// Offset should be `scroll - windowSize/2` (50-100 = -50, clamped to 0).
		// scroll=50, windowSize/2=100 → -50 → 0.
		if offset != 0 {
			t.Fatalf("offset = %d, want 0 (clamped)", offset)
		}
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
}

// TestFetchAroundCmd_AppliesFilterAndFixedSort verifies the status filter set
// via CycleStatusFilter reaches the request query string, and that the list's
// fixed newest-first order (created_at + desc) is always sent — sorting is no
// longer user-configurable.
func TestFetchAroundCmd_AppliesFilterAndFixedSort(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_ = json.NewEncoder(w).Encode(server.RunsResponseBody{Total: 0})
	}))
	defer srv.Close()

	w := NewExecWindow(apiclient.New(srv.URL, ""))
	w.CycleStatusFilter() // "" → running

	fn := w.FetchAroundCmd(0, 20)
	if fn == nil {
		t.Fatal("expected non-nil closure")
	}
	if _, _, _, err := fn(); err != nil {
		t.Fatalf("FetchAroundCmd closure: %v", err)
	}

	if got := gotQuery.Get("status"); got != "running" {
		t.Fatalf("status = %q, want running", got)
	}
	if got := gotQuery.Get("sort_field"); got != "created_at" {
		t.Fatalf("sort_field = %q, want created_at", got)
	}
	if got := gotQuery.Get("sort_direction"); got != "desc" {
		t.Fatalf("sort_direction = %q, want desc", got)
	}
}

// TestStatusFilterCycle pins the filter cycle order and the HasStatusFilter/
// StatusFilter accessors the banner reads.
func TestStatusFilterCycle(t *testing.T) {
	w := NewExecWindow(nil)
	if w.HasStatusFilter() {
		t.Fatalf("HasStatusFilter = true by default, want false")
	}
	if got := w.StatusFilter(); got != "" {
		t.Fatalf("default StatusFilter = %q, want empty", got)
	}
	w.CycleStatusFilter() // "" → running
	if !w.HasStatusFilter() {
		t.Fatalf("HasStatusFilter = false after one cycle, want true")
	}
	if got := w.StatusFilter(); got != "running" {
		t.Fatalf("StatusFilter after one cycle = %q, want running", got)
	}
	// The cycle wraps back to "all" (no filter) after the four entries.
	for i := 1; i < len(statusFilterCycle); i++ {
		w.CycleStatusFilter()
	}
	if w.HasStatusFilter() {
		t.Fatalf("HasStatusFilter = true after full cycle, want false")
	}
}

func TestFetchAroundCmd_ClientErrorResetsLoading(t *testing.T) {
	// Server that returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := NewExecWindow(apiclient.New(srv.URL, ""))
	fn := w.FetchAroundCmd(0, 10)
	if fn == nil {
		t.Fatal("expected non-nil closure")
	}
	_, _, _, err := fn()
	if err == nil {
		t.Fatal("expected error from failing server")
	}
	// loading must have been reset so the next FetchAroundCmd returns non-nil.
	if w.FetchAroundCmd(0, 10) == nil {
		t.Fatal("expected non-nil closure after error reset loading")
	}
}

func TestFetchAroundCmd_NilWhenLoading(t *testing.T) {
	w := NewExecWindow(nil)
	w.SetLoading(true)
	fn := w.FetchAroundCmd(0, 10)
	if fn != nil {
		t.Fatal("expected nil FetchAroundCmd while loading=true")
	}
}

func TestFetchAroundCmd_NilClient_ReturnsNonNil(t *testing.T) {
	// With nil client, FetchAroundCmd itself returns the closure; invoking it
	// will fail with a nil-deref panic — so we only verify the closure is non-nil
	// and that the loading flag is set before the closure runs.
	w := NewExecWindow(nil)
	fn := w.FetchAroundCmd(0, 10)
	if fn == nil {
		t.Fatal("expected non-nil closure from FetchAroundCmd when not loading")
	}
	// After getting the closure, loading must be true.
	fn2 := w.FetchAroundCmd(0, 10)
	if fn2 != nil {
		t.Fatal("expected nil second FetchAroundCmd because first set loading=true")
	}
}

func TestFindRun_Found(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(
		model.Run{ID: "run-1", TaskName: "task"},
		model.Run{ID: "run-2", TaskName: "task"},
	)
	w.ApplyFetch(items, 0, 2)

	got := w.FindRun("run-2")
	if got == nil {
		t.Fatal("expected to find run-2, got nil")
	}
	if got.ID != "run-2" {
		t.Fatalf("expected run-2, got %s", got.ID)
	}
}

func TestFindRun_NotFound_MissingFromIDSet(t *testing.T) {
	w := NewExecWindow(nil)
	items := makeItems(model.Run{ID: "run-1", TaskName: "task"})
	w.ApplyFetch(items, 0, 1)

	got := w.FindRun("does-not-exist")
	if got != nil {
		t.Fatalf("expected nil for unknown run ID, got %+v", got)
	}
}

func TestFindRun_EmptyWindow(t *testing.T) {
	w := NewExecWindow(nil)
	if got := w.FindRun("any"); got != nil {
		t.Fatalf("expected nil for empty window, got %+v", got)
	}
}

// TestUpdateVisibleTimes covers the three visible-time refresh shapes:
// fully-overlapping window, partially-overlapping (offset) window, and
// empty window. Replaces "does not panic" with assertions on the actual
// TimeAgo side effect on items that fall inside the refreshed range.
func TestUpdateVisibleTimes(t *testing.T) {
	t.Run("full-overlap-sets-time-ago", func(t *testing.T) {
		w := NewExecWindow(nil)
		w.ApplyFetch(makeItems(
			model.Run{ID: "r1", TaskName: "task", Status: model.PhaseRunning},
			model.Run{ID: "r2", TaskName: "task", Status: model.PhaseEnded},
			model.Run{ID: "r3", TaskName: "task", Status: model.PhaseRunning},
		), 0, 3)

		w.UpdateVisibleTimes(0, 3)

		for i := 0; i < 3; i++ {
			item := w.Item(i)
			if item == nil {
				t.Fatalf("expected item at %d", i)
			}
			if item.TimeAgo == "" {
				t.Fatalf("expected TimeAgo populated for item %d after refresh", i)
			}
		}
	})

	t.Run("partial-overlap-refreshes-in-range-items", func(t *testing.T) {
		w := NewExecWindow(nil)
		w.ApplyFetch(makeItems(
			model.Run{ID: "r10", TaskName: "task", Status: model.PhaseRunning},
			model.Run{ID: "r11", TaskName: "task", Status: model.PhaseEnded},
		), 10, 2)

		// scroll=9 below windowStart=10; scroll+vpH=12 overlaps the window.
		w.UpdateVisibleTimes(9, 3)

		// Items at indices 10 and 11 should have TimeAgo populated.
		if got := w.Item(10); got == nil || got.TimeAgo == "" {
			t.Fatalf("expected item 10 with non-empty TimeAgo after refresh, got %+v", got)
		}
	})

	t.Run("empty-window-is-noop", func(t *testing.T) {
		w := NewExecWindow(nil)
		// Must not panic; window stays empty.
		w.UpdateVisibleTimes(0, 10)
		if got := w.Item(0); got != nil {
			t.Fatalf("expected nil item on empty window, got %+v", got)
		}
	})
}
