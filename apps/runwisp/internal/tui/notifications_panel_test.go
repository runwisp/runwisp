// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
)

func TestNotificationsPanel_UpsertNew(t *testing.T) {
	p := newNotificationsPanel()

	changed := p.Upsert(apiclient.Notification{
		ID:             "01HX",
		Title:          "backup-db failed",
		Severity:       "error",
		Count:          1,
		LastOccurredAt: time.Now(),
	})
	if !changed {
		t.Fatal("expected first upsert to mark the panel changed")
	}
	if p.Total() != 1 {
		t.Fatalf("Total: want 1, got %d", p.Total())
	}
	if p.Unread() != 1 {
		t.Fatalf("Unread should bump on first insert; got %d", p.Unread())
	}
}

func TestNotificationsPanel_UpsertSameTimestampNoOp(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	n := apiclient.Notification{ID: "x", Severity: "info", Count: 1, LastOccurredAt: now, Title: "t"}
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

	p.Upsert(apiclient.Notification{ID: "01AAAA", Severity: "info", Count: 1, LastOccurredAt: now.Add(-time.Hour), Title: "old"})
	p.Upsert(apiclient.Notification{ID: "01ZZZZ", Severity: "warn", Count: 1, LastOccurredAt: now, Title: "new"})

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
	p.Upsert(apiclient.Notification{
		ID:             "01HX",
		Title:          "backup-db failed",
		Severity:       "error",
		Count:          3,
		LastOccurredAt: time.Now(),
	})

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

func TestNotificationsPanel_UpsertCoalesceIncrementsUnread(t *testing.T) {
	p := newNotificationsPanel()
	now := time.Now()
	p.Upsert(apiclient.Notification{ID: "x", Severity: "warn", Count: 1, LastOccurredAt: now, Title: "t"})
	if p.Unread() != 1 {
		t.Fatalf("Unread after first insert: want 1, got %d", p.Unread())
	}
	if !p.Upsert(apiclient.Notification{ID: "x", Severity: "warn", Count: 4, LastOccurredAt: now.Add(time.Minute), Title: "t"}) {
		t.Fatal("upserting an updated count should mark the panel changed")
	}
	if p.Unread() != 4 {
		t.Fatalf("Unread should grow by the count delta; got %d", p.Unread())
	}
}

func TestNotificationsPanel_ToggleAndExpandedHeight(t *testing.T) {
	p := newNotificationsPanel()
	p.SetWidth(80)
	p.Upsert(apiclient.Notification{ID: "01H1", Severity: "error", Count: 1, LastOccurredAt: time.Now(), Title: "boom"})

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
	p.Upsert(apiclient.Notification{ID: "01A", Severity: "info", Count: 1, LastOccurredAt: now.Add(-2 * time.Hour), Title: "first"})
	p.Upsert(apiclient.Notification{ID: "01B", Severity: "warn", Count: 1, LastOccurredAt: now.Add(-time.Hour), Title: "second"})
	p.Upsert(apiclient.Notification{ID: "01C", Severity: "error", Count: 1, LastOccurredAt: now, Title: "third"})

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

func TestNotificationsPanel_MarkAllReadLocalClearsUnread(t *testing.T) {
	p := newNotificationsPanel()
	p.Upsert(apiclient.Notification{ID: "x", Severity: "warn", Count: 1, LastOccurredAt: time.Now(), Title: "t"})
	if p.Unread() != 1 {
		t.Fatalf("Unread before mark-read: want 1, got %d", p.Unread())
	}
	p.MarkAllReadLocal(time.Now())
	if p.Unread() != 0 {
		t.Fatalf("Unread after mark-read: want 0, got %d", p.Unread())
	}
}

func TestNotificationsPanel_UnreadGuardedByLastReadAt(t *testing.T) {
	p := newNotificationsPanel()
	t0 := time.Now()
	// Mark a future moment as read first.
	p.MarkAllReadLocal(t0.Add(time.Hour))
	// An older item should NOT bump unread because lastReadAt is in the future.
	p.Upsert(apiclient.Notification{ID: "old", Severity: "warn", Count: 1, LastOccurredAt: t0, Title: "stale"})
	if p.Unread() != 0 {
		t.Fatalf("Unread should remain 0 for items older than lastReadAt; got %d", p.Unread())
	}
	// A newer item SHOULD bump unread.
	p.Upsert(apiclient.Notification{ID: "new", Severity: "warn", Count: 1, LastOccurredAt: t0.Add(2 * time.Hour), Title: "fresh"})
	if p.Unread() != 1 {
		t.Fatalf("Unread should bump for items newer than lastReadAt; got %d", p.Unread())
	}
}

func TestNotificationsPanel_SetUnread(t *testing.T) {
	p := newNotificationsPanel()
	p.SetUnread(7)
	if p.Unread() != 7 {
		t.Fatalf("SetUnread: want 7, got %d", p.Unread())
	}
	if p.PanelHeight() != notificationsCollapsedH {
		t.Fatal("SetUnread alone should make the panel visible at collapsed height")
	}
}
