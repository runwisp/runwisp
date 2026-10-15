// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/execlist"
)

// newReadOnlyModel is newTestModel signed in with the read-only password.
func newReadOnlyModel(tasks []model.Task) Model {
	client := newDummyClient()
	client.SetToken("4102444800.viewer.mac")
	return NewModel(TUIConfig{Client: client, Info: uikit.StartupInfo{Version: "0.0.0-test", Tasks: tasks}})
}

// A read-only session offers no write: no task buttons, no write keys, and no
// write hints in the help bar or the help overlay.
func TestReadOnly_HidesWriteActions(t *testing.T) {
	tasks := []model.Task{
		{Name: "backup", Cron: "0 3 * * *", ManualTrigger: true},
		{Name: "web", Kind: model.KindService, ManualTrigger: true},
	}
	m := newReadOnlyModel(tasks)
	require.True(t, m.readOnly)
	assert.Contains(t, m.sidebar.View(), "read-only")
	assert.False(t, newTestModel(tasks).readOnly)

	for _, item := range []int{1, 2} {
		selectSidebarItem(&m, item)
		m.focusMainPanel()
		assert.Empty(t, m.taskHeader(m.sidebar.ActiveTask()).Hints())
		for _, key := range []string{"r", "R", "s", "p"} {
			got, _ := m.handleKey(keyMsg(key))
			gm := got.(Model)
			assert.False(t, gm.dialogs.Has(dlgConfirm), "key %s", key)
			assert.False(t, gm.dialogs.Has(dlgParamForm), "key %s", key)
		}
		bar := m.buildHelpText().String()
		for _, hint := range []string{"r run now", "r restart", "s stop", "p pause"} {
			assert.NotContains(t, bar, hint)
		}
	}

	m.notifications.Upsert(testNotif("n1"))
	m.notifications.Toggle()
	assert.NotContains(t, m.buildHelpText().String(), "mark read")
	assert.NotContains(t, m.notifications.View(), "mark read")

	for _, section := range helpSections(true) {
		for _, b := range section.Bindings {
			assert.False(t, b.Writes, "overlay lists %q", b.Desc)
		}
	}
	assert.True(t, hasWriteBinding(helpSections(false)))
}

func hasWriteBinding(sections []keys.Section) bool {
	for _, section := range sections {
		for _, b := range section.Bindings {
			if b.Writes {
				return true
			}
		}
	}
	return false
}

// An open run in a read-only session has no Stop/Retry/Delete button, and its
// notifications stay unread rather than failing a mark-read the daemon rejects.
func TestReadOnly_ExecViewHasNoActions(t *testing.T) {
	m := newReadOnlyModel([]model.Task{{Name: "t1"}})
	notif := testNotif("n1")
	notif.RunID = "run-1"
	m.notifications.Upsert(notif)
	reason := model.ReasonFailed
	run := &model.Run{ID: "run-1", TaskName: "t1", Status: model.PhaseEnded, EndReason: &reason}
	m.openExecView(run)

	assert.Equal(t, execlist.ActionNone, m.execView.Action())
	assert.False(t, m.execView.CanDelete())
	assert.NotEmpty(t, m.notifications.UnreadIDsForRun("run-1"), "opening the run must not mark its notification read")
	bar := m.buildHelpText().String()
	for _, hint := range []string{"r retry", "D delete", "s stop"} {
		assert.NotContains(t, bar, hint)
	}
}
