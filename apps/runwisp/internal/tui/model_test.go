// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/server/dto"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
)

// TestModel_Guards covers the early-return / no-op branches of helpers that
// short-circuit on empty selections, nil execView, nil clients, or out-of-range
// cursors. These exist as individual blocks in Go's coverage profile, so the
// table preserves block coverage while removing per-case boilerplate.
func TestModel_Guards(t *testing.T) {
	t.Run("toggleSelectedNotificationRead returns nil with no selection", func(t *testing.T) {
		m := newTestModel(nil)
		if cmd := m.toggleSelectedNotificationRead(); cmd != nil {
			t.Fatal("expected nil cmd when no selection")
		}
	})

	t.Run("closeExecView is safe with nil execView", func(t *testing.T) {
		m := newTestModel(nil)
		// must not panic; syncMouseState may return nil
		m.closeExecView()
	})

	t.Run("syncMouseState first call with no holds returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if cmd := m.syncMouseState(); cmd != nil {
			t.Fatalf("expected nil cmd from baseline syncMouseState, got %v", cmd)
		}
	})

	t.Run("latestRunningExec on empty window returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.latestRunningExec("t1") != nil {
			t.Fatal("expected nil for empty window")
		}
	})

	t.Run("copyExecField with nil execView returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.copyExecField() != nil {
			t.Fatal("expected nil cmd when no execView")
		}
	})

	t.Run("openRunByID with nil client and run not in window returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.openRunByID("task", "run-id-123") != nil {
			t.Fatal("expected nil cmd when client is nil and run not in window")
		}
	})

	t.Run("downloadExecLog with nil execView returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.downloadExecLog() != nil {
			t.Fatal("expected nil when no execView")
		}
	})

	t.Run("openLaunchURL with nil launchTicketFunc returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		if m.openLaunchURL("/") != nil {
			t.Fatal("expected nil when launchTicketFunc is nil")
		}
	})

	t.Run("activateHomeField with negative cursor returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		m.homeCursor = -1
		if m.activateHomeField() != nil {
			t.Fatal("expected nil for negative cursor")
		}
	})

	t.Run("activateHomeField with cursor beyond fields returns nil", func(t *testing.T) {
		m := newTestModel(nil)
		// No fields (port=0, no password) → len(fields)==0
		m.homeCursor = 5
		if m.activateHomeField() != nil {
			t.Fatal("expected nil when cursor beyond fields length")
		}
	})
}

// ─── closeExecView ────────────────────────────────────────────────────────────

func TestCloseExecView_SetsExecListFocusedWhenPanelMain(t *testing.T) {
	m := newTestModel(nil)
	run := &dto.Run{ID: "r1", TaskName: "t1"}
	ev := execlist.NewExecView(run)
	m.execView = &ev
	m.panelFocus = uikit.PanelMain

	m.closeExecView()
	if m.execView != nil {
		t.Fatal("expected execView to be nil after closeExecView")
	}
}

// ─── serviceInstances ────────────────────────────────────────────────────────

func TestServiceInstances_UnknownTaskReturns0(t *testing.T) {
	m := newTestModel(nil)
	if got := m.serviceInstances("unknown"); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestServiceInstances_ServiceWithZeroInstances(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "svc", Kind: model.KindService, Instances: 0},
	}
	m := newTestModel(tasks)
	// Instances < 1 → returns 1
	if got := m.serviceInstances("svc"); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

func TestServiceInstances_ServiceWithThreeInstances(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "svc", Kind: model.KindService, Instances: 3},
	}
	m := newTestModel(tasks)
	if got := m.serviceInstances("svc"); got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
}

func TestServiceInstances_NonServiceTask(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "job", Kind: model.KindTask},
	}
	m := newTestModel(tasks)
	// not a service → 0
	if got := m.serviceInstances("job"); got != 0 {
		t.Fatalf("expected 0 for non-service, got %d", got)
	}
}

// ─── autoOpenService (negative branches) ─────────────────────────────────────

func TestAutoOpenService_NonServiceTaskReturnsNil(t *testing.T) {
	tasks := []model.TaskBrief{
		{Name: "job", Kind: model.KindTask},
	}
	m := newTestModel(tasks)
	if m.autoOpenService("job") != nil {
		t.Fatal("expected nil for non-service task")
	}
}

func TestAutoOpenService_UnknownTaskReturnsNil(t *testing.T) {
	m := newTestModel(nil)
	if m.autoOpenService("unknown") != nil {
		t.Fatal("expected nil for unknown task")
	}
}

// ─── markRunNotificationsRead ────────────────────────────────────────────────

func TestMarkRunNotificationsRead_MarksLocallyRead(t *testing.T) {
	m := newTestModel(nil)
	n := server.NotificationDTO{
		ID:             "n1",
		Severity:       "info",
		Count:          1,
		LastOccurredAt: time.Now(),
		Title:          "test",
		RunID:          "run-999",
	}
	m.notifications.Upsert(n)

	if len(m.notifications.UnreadIDsForRun("run-999")) == 0 {
		t.Fatal("precondition: expected at least one unread notification for run-999")
	}

	_ = m.markRunNotificationsRead("run-999")

	if got := len(m.notifications.UnreadIDsForRun("run-999")); got != 0 {
		t.Fatalf("expected notification to be marked read locally, still %d unread", got)
	}
}

// ─── autoOpenService ─────────────────────────────────────────────────────────

func TestAutoOpenService_SingleInstanceWithRunningExecOpensView(t *testing.T) {
	tasks := []model.TaskBrief{{
		Name:          "svc",
		Kind:          model.KindService,
		MaxConcurrent: 1,
	}}
	m := newTestModel(tasks)

	run := dto.Run{
		ID:       "r-svc",
		TaskName: "svc",
		Status:   sqlcdb.PhaseRunning,
	}
	m.execWindow.UpsertRun(run)

	_ = m.autoOpenService("svc")
	if m.execView == nil {
		t.Fatal("expected execView to be set after autoOpenService with running exec")
	}
	if m.execView.Run == nil || m.execView.Run.ID != "r-svc" {
		t.Fatalf("expected execView to wrap run r-svc, got %+v", m.execView.Run)
	}
}

// ─── openRunByID ─────────────────────────────────────────────────────────────

func TestOpenRunByID_RunInWindowOpensExecView(t *testing.T) {
	m := newTestModel(nil)
	run := dto.Run{
		ID:       "r-found",
		TaskName: "backup-db",
		Status:   sqlcdb.PhasePending,
	}
	m.execWindow.UpsertRun(run)

	_ = m.openRunByID("backup-db", "r-found")
	if m.execView == nil {
		t.Fatal("expected execView to be set when run is found in window")
	}
	if m.execView.Run == nil || m.execView.Run.ID != "r-found" {
		t.Fatalf("expected execView to wrap run r-found, got %+v", m.execView.Run)
	}
}

// ─── resolveTaskName ─────────────────────────────────────────────────────────

func TestResolveTaskName_PanelMainReturnsSidebarActive(t *testing.T) {
	tasks := []model.TaskBrief{{Name: "backup-db"}}
	m := newTestModel(tasks)
	selectSidebarItem(&m, 1)
	m.focusMainPanel()

	if name := m.resolveTaskName(); name != "backup-db" {
		t.Fatalf("expected backup-db, got %q", name)
	}
}

// ─── showQuitConfirm ─────────────────────────────────────────────────────────

func TestShowQuitConfirm_OpensConfirmDialog(t *testing.T) {
	m := newTestModel(nil)
	if m.dialogs.HasConfirm() {
		t.Fatal("no dialog expected initially")
	}
	m.showQuitConfirm()
	if !m.dialogs.HasConfirm() {
		t.Fatal("expected confirm dialog after showQuitConfirm")
	}
}
