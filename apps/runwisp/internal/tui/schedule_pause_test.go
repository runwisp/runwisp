// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pauseTestTasks() []model.Task {
	return []model.Task{
		{Name: "backup", Kind: model.KindTask, Cron: "0 3 * * *", ManualTrigger: true},
		{Name: "locked", Kind: model.KindTask, Cron: "0 3 * * *"},
		{Name: "held", Kind: model.KindTask, Cron: "0 3 * * *", ManualTrigger: true, HeldBy: model.HeldByCron},
		{Name: "adhoc", Kind: model.KindTask, ManualTrigger: true},
		{Name: "web", Kind: model.KindService, ManualTrigger: true},
	}
}

func TestStreamManager_FetchPausedTasks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"backup","pausedAt":"2026-09-29T10:00:00Z"},{"name":"other"}]}`))
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	msg, ok := sm.FetchPausedTasks()().(uikit.PausedTasksMsg)
	require.True(t, ok)
	require.NoError(t, msg.Err)
	assert.Equal(t, map[string]time.Time{"backup": time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)}, msg.Paused)
}

func TestStreamManager_SetSchedulePaused(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	assert.Equal(t, uikit.SchedulePauseMsg{TaskName: "backup", Paused: true}, sm.SetSchedulePaused("backup", true)())
	assert.Equal(t, uikit.SchedulePauseMsg{TaskName: "backup"}, sm.SetSchedulePaused("backup", false)())
	assert.Equal(t, []string{"POST /api/tasks/backup/pause", "POST /api/tasks/backup/resume"}, paths)
}

// `p` acts on cron tasks only; a locked or held one still goes to the daemon so
// its refusal can name the reason.
func TestHandleKeyP_OnlyCronTasks(t *testing.T) {
	for name, handled := range map[string]bool{"backup": true, "locked": true, "held": true, "adhoc": false, "web": false} {
		m := newTestModel(pauseTestTasks())
		m.panelFocus = uikit.PanelSidebar
		m.sidebar.Rebuild(pauseTestTasks())
		for m.sidebar.CursorTaskName() != name {
			m.sidebar.MoveCursor(1)
		}
		_, cmd, ok := handleKeyP(m, tea.KeyPressMsg{Code: 'p', Text: "p"})
		assert.Equal(t, handled, ok, name)
		assert.Equal(t, handled, cmd != nil, name)
	}
}

func TestHandleSchedulePause_FlipsHeaderAndArmsUndo(t *testing.T) {
	m := newTestModel(pauseTestTasks())

	updated, cmd := m.handleSchedulePause(uikit.SchedulePauseMsg{TaskName: "backup", Paused: true})
	require.NotNil(t, cmd)
	got := updated.(Model)
	assert.True(t, got.isPaused("backup"))
	assert.False(t, m.isPaused("backup"), "the previous model's map must not be mutated")
	flash, _ := got.dialogs.FlashActive()
	assert.Contains(t, flash, "Paused the schedule of 'backup'")
	assert.NotNil(t, got.dialogs.TakeUndo(), "a pause is undoable")

	updated, _ = got.handleSchedulePause(uikit.SchedulePauseMsg{TaskName: "backup"})
	resumed := updated.(Model)
	assert.False(t, resumed.isPaused("backup"))
}

func TestHandleSchedulePause_ErrorShowsDaemonDetail(t *testing.T) {
	m := newTestModel(pauseTestTasks())
	err := &apiclient.HTTPStatusError{StatusCode: http.StatusConflict, Body: `{"detail":"task \"held\" is held by cron"}`}

	updated, _ := m.handleSchedulePause(uikit.SchedulePauseMsg{TaskName: "held", Paused: true, Err: err})
	got := updated.(Model)
	flash, _ := got.dialogs.FlashActive()
	assert.Equal(t, `Pause failed: task "held" is held by cron`, flash)
	assert.False(t, got.isPaused("held"))
	assert.Nil(t, got.dialogs.TakeUndo())
}

func TestPauseHint(t *testing.T) {
	m := newTestModel(pauseTestTasks())
	m.info.PausedTasks = map[string]time.Time{"held": time.Now()}

	assert.Equal(t, keys.Pause.Bar, m.pauseHint("backup"))
	assert.Equal(t, keys.Resume.Bar, m.pauseHint("held"), "a paused task can be resumed even while held")
	for _, name := range []string{"locked", "adhoc", "web", "missing"} {
		assert.Empty(t, m.pauseHint(name), name)
	}
	delete(m.info.PausedTasks, "held")
	assert.Empty(t, m.pauseHint("held"), "a held task can't be paused")
}
