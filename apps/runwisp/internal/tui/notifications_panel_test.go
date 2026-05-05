// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
)

func unreadNotification(id string, sev string, occurredAt time.Time, title string) apiclient.Notification {
	return apiclient.Notification{
		ID:             id,
		Severity:       sev,
		Count:          1,
		LastOccurredAt: occurredAt,
		Title:          title,
	}
}

func readNotification(id string, sev string, occurredAt time.Time, title string) apiclient.Notification {
	stamp := occurredAt
	n := unreadNotification(id, sev, occurredAt, title)
	n.ReadAt = &stamp
	return n
}

func TestNotificationsPanel_UpsertNew(t *testing.T) {
	p := newNotificationsPanel()

	changed := p.Upsert(unreadNotification("01HX", "error", time.Now(), "backup-db failed"))
	if !changed {
		t.Fatal("expected first upsert to mark the panel changed")
	}
	if p.Total() != 1 {
		t.Fatalf("Total: want 1, got %d", p.Total())
	}
	if p.Unread() != 1 {
		t.Fatalf("Unread should reflect the unread row; got %d", p.Unread())
	}
}

func TestNotificationsPanel_UpsertSameTimestampNoOp(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	n := unreadNotification("x", "info", now, "t")
	if !p.Upsert(n) {
		t.Fatal("first upsert should change panel")
	}
	if p.Upsert(n) {
		t.Fatal("identical re-upsert should be a no-op")
	}
}

func TestNotificationsPanel_OrderedDescending(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()

	p.Upsert(unreadNotification("01AAAA", "info", now.Add(-time.Hour), "old"))
	p.Upsert(unreadNotification("01ZZZZ", "warn", now, "new"))

	p.Toggle()
	if got := p.Selected(); got == nil || got.ID != "01ZZZZ" {
		t.Fatalf("Selected on first toggle should be the higher ULID; got %v", got)
	}
}

func TestNotificationsPanel_PanelHeightEmpty(t *testing.T) {
	p := newNotificationsPanel()
	if got := p.PanelHeight(); got != 0 {
		t.Fatalf("empty panel should not reserve space; got %d", got)
	}
	if got := p.View(); got != "" {
		t.Fatalf("empty panel should render empty string; got %q", got)
	}
}

func TestNotificationsPanel_CollapsedView(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(120)
	n := unreadNotification("01HX", "error", time.Now(), "backup-db failed")
	n.Count = 3
	p.Upsert(n)

	if p.PanelHeight() != notificationsCollapsedH {
		t.Fatalf("collapsed PanelHeight: want %d, got %d", notificationsCollapsedH, p.PanelHeight())
	}
	view := p.View()
	if !strings.Contains(view, "backup-db failed") {
		t.Errorf("collapsed view should include the title, got %q", view)
	}
	if !strings.Contains(view, "press n to expand") {
		t.Errorf("collapsed view should hint at expand keybinding, got %q", view)
	}
}

func TestNotificationsPanel_UpsertCoalesceRepaints(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	p.Upsert(unreadNotification("x", "warn", now, "t"))
	if p.Unread() != 1 {
		t.Fatalf("Unread after first insert: want 1, got %d", p.Unread())
	}
	updated := unreadNotification("x", "warn", now.Add(time.Minute), "t")
	updated.Count = 4
	if !p.Upsert(updated) {
		t.Fatal("upserting an updated count should mark the panel changed")
	}
	if p.Unread() != 1 {
		t.Fatalf("Unread is per-row, not per-occurrence; got %d", p.Unread())
	}
}

func TestNotificationsPanel_ToggleAndExpandedHeight(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(80)
	p.Upsert(unreadNotification("01H1", "error", time.Now(), "boom"))

	if p.IsExpanded() {
		t.Fatal("panel should start collapsed")
	}
	p.Toggle()
	if !p.IsExpanded() {
		t.Fatal("Toggle should expand the panel")
	}
	if p.PanelHeight() != notificationsExpandedH {
		t.Fatalf("expanded PanelHeight: want %d, got %d", notificationsExpandedH, p.PanelHeight())
	}
	view := p.View()
	if !strings.Contains(view, "boom") {
		t.Errorf("expanded view should include item title; got %q", view)
	}
	if !strings.Contains(view, "n collapse") {
		t.Errorf("expanded view should show collapse hint; got %q", view)
	}
}

func TestNotificationsPanel_MoveCursorBounded(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(80)
	now := time.Now()
	p.Upsert(unreadNotification("01A", "info", now.Add(-2*time.Hour), "first"))
	p.Upsert(unreadNotification("01B", "warn", now.Add(-time.Hour), "second"))
	p.Upsert(unreadNotification("01C", "error", now, "third"))

	// Collapsed → no movement.
	if p.MoveCursor(1) {
		t.Fatal("MoveCursor on collapsed panel should be a no-op")
	}

	p.Toggle()
	if got := p.Selected(); got == nil || got.ID != "01C" {
		t.Fatalf("initial selection should be the newest item; got %v", got)
	}
	if !p.MoveCursor(1) {
		t.Fatal("MoveCursor down should advance")
	}
	if got := p.Selected(); got == nil || got.ID != "01B" {
		t.Fatalf("after one down: want 01B, got %v", got)
	}
	// Move past the bottom — capped at the last item.
	p.MoveCursor(99)
	if got := p.Selected(); got == nil || got.ID != "01A" {
		t.Fatalf("MoveCursor should clamp to the last item; got %v", got)
	}
	// Move past the top — capped at the first item.
	p.MoveCursor(-99)
	if got := p.Selected(); got == nil || got.ID != "01C" {
		t.Fatalf("MoveCursor should clamp to the first item; got %v", got)
	}
}

func TestNotificationsPanel_MarkReadLocalClearsUnread(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	p.Upsert(unreadNotification("x", "warn", now, "t"))
	if p.Unread() != 1 {
		t.Fatalf("Unread before mark-read: want 1, got %d", p.Unread())
	}
	if !p.MarkReadLocal("x", now) {
		t.Fatal("MarkReadLocal on a known unread row must succeed")
	}
	if p.Unread() != 0 {
		t.Fatalf("Unread after mark-read: want 0, got %d", p.Unread())
	}
	// Idempotent: re-marking is a no-op.
	if p.MarkReadLocal("x", now) {
		t.Fatal("MarkReadLocal on an already-read row must be a no-op")
	}
}

func TestNotificationsPanel_MarkUnreadLocalRestoresUnread(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	p.Upsert(readNotification("x", "warn", now, "t"))
	if p.Unread() != 0 {
		t.Fatalf("Read row must not count toward Unread; got %d", p.Unread())
	}
	if !p.MarkUnreadLocal("x") {
		t.Fatal("MarkUnreadLocal on a read row must succeed")
	}
	if p.Unread() != 1 {
		t.Fatalf("Unread after mark-unread: want 1, got %d", p.Unread())
	}
}

func TestNotificationsPanel_SetUnreadHint(t *testing.T) {
	p := newNotificationsPanel()
	p.SetUnreadHint(7)
	if p.Unread() != 7 {
		t.Fatalf("SetUnreadHint: want 7, got %d", p.Unread())
	}
	if p.PanelHeight() != notificationsCollapsedH {
		t.Fatal("SetUnreadHint alone should make the panel visible at collapsed height")
	}
}

func TestNotificationsPanel_LoadHistoricalUsesItemReadState(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	items := []apiclient.Notification{
		readNotification("01A", "info", now.Add(-time.Hour), "old-read"),
		unreadNotification("01B", "warn", now, "new-unread"),
	}
	if !p.LoadHistorical(items) {
		t.Fatal("LoadHistorical with new items should mark the panel changed")
	}
	if p.Total() != 2 {
		t.Fatalf("Total after LoadHistorical: want 2, got %d", p.Total())
	}
	if p.Unread() != 1 {
		t.Fatalf("Unread should reflect per-row state from the loaded page; got %d", p.Unread())
	}

	// Re-loading the same items is a no-op.
	if p.LoadHistorical(items) {
		t.Fatal("LoadHistorical with already-known items should be a no-op")
	}

	// Ordered DESC by ID — newest selected first when expanded.
	p.Toggle()
	if got := p.Selected(); got == nil || got.ID != "01B" {
		t.Fatalf("Selected should be highest ULID; got %v", got)
	}
}

func TestNotificationsPanel_HintFloorYieldsToDerivedCount(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	// Unread snapshot says "3 unread"; the loaded page only has one unread row.
	p.SetUnreadHint(3)
	p.LoadHistorical([]apiclient.Notification{
		unreadNotification("01A", "info", now, "loaded"),
	})
	if p.Unread() != 3 {
		t.Fatalf("Hint floor must shadow derivation while it's higher; got %d", p.Unread())
	}
	// Marking the only loaded row read drops the floor to zero (per-row state
	// has been touched, so the snapshot is stale).
	p.MarkReadLocal("01A", now)
	if p.Unread() != 0 {
		t.Fatalf("After explicit local change the floor must be cleared; got %d", p.Unread())
	}
}

func TestNotificationsPanel_UnreadIDsForRun(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	a := unreadNotification("01A", "error", now, "fail-1")
	a.RunID = "run-1"
	b := unreadNotification("01B", "error", now, "fail-1-again")
	b.RunID = "run-1"
	c := readNotification("01C", "error", now, "old-fail")
	c.RunID = "run-1"
	d := unreadNotification("01D", "error", now, "other-run")
	d.RunID = "run-2"
	p.Upsert(a)
	p.Upsert(b)
	p.Upsert(c)
	p.Upsert(d)

	got := p.UnreadIDsForRun("run-1")
	wantSet := map[string]struct{}{"01A": {}, "01B": {}}
	if len(got) != len(wantSet) {
		t.Fatalf("UnreadIDsForRun(run-1): want %d ids, got %d (%v)", len(wantSet), len(got), got)
	}
	for _, id := range got {
		if _, ok := wantSet[id]; !ok {
			t.Errorf("unexpected id in result: %s", id)
		}
	}
	if len(p.UnreadIDsForRun("")) != 0 {
		t.Error("empty runID should yield no ids")
	}
	if len(p.UnreadIDsForRun("run-3")) != 0 {
		t.Error("unknown runID should yield no ids")
	}
}

func TestNotificationsPanel_MoveCursorBoundaryDoesNotMove(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(80)
	p.Upsert(unreadNotification("01H1", "error", time.Now(), "only"))
	p.Toggle()
	if p.MoveCursor(1) {
		t.Fatal("MoveCursor on a single-item list should be a no-op (no boundary movement)")
	}
	if p.MoveCursor(-1) {
		t.Fatal("MoveCursor up at the top should be a no-op")
	}
}

func TestNotificationsPanel_BumpBoundaryFlashSetsTimestamp(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(80)
	p.Upsert(unreadNotification("01H1", "error", time.Now(), "only"))
	p.Toggle()

	if p.flashActive() {
		t.Fatal("flash should be inactive before any bump")
	}
	p.BumpBoundaryFlash()
	if !p.flashActive() {
		t.Fatal("flashActive should be true immediately after BumpBoundaryFlash")
	}
	// Wait past the flash window and verify it has elapsed.
	time.Sleep(notificationsBoundaryFlashDuration + 50*time.Millisecond)
	if p.flashActive() {
		t.Fatal("flashActive should be false after the duration elapses")
	}
	// ClearBoundaryFlash zeroes the timestamp once the window is in the past.
	p.ClearBoundaryFlash()
	if !p.flashCursorUntil.IsZero() {
		t.Fatal("ClearBoundaryFlash should zero flashCursorUntil after the window elapsed")
	}
}

func TestNotificationsPanel_ClearBoundaryFlashIdempotent(t *testing.T) {
	p := newNotificationsPanel()
	// Never bumped — clearing must be a safe no-op.
	p.ClearBoundaryFlash()
	if !p.flashCursorUntil.IsZero() {
		t.Fatal("ClearBoundaryFlash without a prior bump must leave the field zeroed")
	}

	p.SetWidth(80)
	p.Upsert(unreadNotification("01H1", "error", time.Now(), "only"))
	p.Toggle()
	p.BumpBoundaryFlash()
	// Calling Clear while the flash is still in-window must NOT zero the field;
	// rapid repeat keypresses extend it forward and the early Clear-tick is
	// expected to be a no-op so the most recent flash survives.
	p.ClearBoundaryFlash()
	if p.flashCursorUntil.IsZero() {
		t.Fatal("ClearBoundaryFlash inside the flash window must not zero flashCursorUntil")
	}
}

func TestNotificationsPanel_BumpBoundaryFlashNoOpWhenCollapsedOrEmpty(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(80)
	// Empty + collapsed → bump must be a no-op.
	p.BumpBoundaryFlash()
	if p.flashActive() {
		t.Fatal("BumpBoundaryFlash on an empty panel should not start a flash")
	}
	// Items present but collapsed → bump still no-op (only useful when expanded).
	p.Upsert(unreadNotification("01H1", "error", time.Now(), "only"))
	p.BumpBoundaryFlash()
	if p.flashActive() {
		t.Fatal("BumpBoundaryFlash while collapsed should not start a flash")
	}
}

func TestNotificationsPanel_CountLabelShowsUnreadCountOnly(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(80)
	now := time.Now()
	p.LoadHistorical([]apiclient.Notification{
		readNotification("01A", "info", now, "first"),
		unreadNotification("01B", "info", now, "second"),
	})
	view := p.View()
	if !strings.Contains(view, "Notifications (1)") {
		t.Errorf("collapsed view should show single-number badge; got %q", view)
	}
	if strings.Contains(view, "unread") {
		t.Errorf("collapsed view should not contain the word 'unread'; got %q", view)
	}
	// Mark the remaining unread row read → drop the parens entirely so
	// pressing "r" produces visible feedback.
	p.MarkReadLocal("01B", now)
	view = p.View()
	if !strings.Contains(view, "Notifications") {
		t.Errorf("with no unread, collapsed view should still show 'Notifications'; got %q", view)
	}
	if strings.Contains(view, "Notifications (") {
		t.Errorf("with no unread, collapsed view must not show a count badge; got %q", view)
	}
}

func TestNotificationsPanel_ReadItemHidesSeverityDot(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(80)
	t0 := time.Now()
	p.Upsert(readNotification("01A", "error", t0.Add(-time.Hour), "old"))
	p.Upsert(unreadNotification("01B", "error", t0.Add(time.Hour), "fresh"))
	p.Toggle()

	view := p.View()
	if !strings.Contains(view, "fresh") || !strings.Contains(view, "old") {
		t.Fatalf("expected both rows in the view; got %q", view)
	}
	// One ● for the unread row, none for the read row.
	if got := strings.Count(view, "●"); got != 1 {
		t.Errorf("expected exactly one severity dot for the unread row; got %d in %q", got, view)
	}
}
