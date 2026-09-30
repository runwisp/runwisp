// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupPauseServer is the standard test server with a started scheduler that
// owns two cron tasks: "nightly" (pausable) and "locked" (manual_trigger =
// false). task1 from setupServer has no cron.
func setupPauseServer(t *testing.T) *Server {
	t.Helper()
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) {
		tasks := map[string]*model.Task{
			"task1":   {Name: "task1", Run: "echo hi", ManualTrigger: true},
			"nightly": {Name: "nightly", Run: "echo hi", Cron: "0 3 * * *", ManualTrigger: true},
			"locked":  {Name: "locked", Run: "echo hi", Cron: "0 3 * * *"},
		}
		o.Tasks = runtime.NewTaskRegistry(tasks)
		o.Scheduler = runtime.NewScheduler(o.TaskManager, tasks, time.UTC, nil)
		_, err := o.Scheduler.Start()
		require.NoError(t, err)
		t.Cleanup(o.Scheduler.Stop)
	})
	return s
}

func postTask(t *testing.T, s *Server, name, verb string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+name+"/"+verb, nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)
	return w.Code
}

func getTaskResponse(t *testing.T, s *Server, name string) model.TaskResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+name, nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var tr model.TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tr))
	return tr
}

func TestPauseResumeHTTP(t *testing.T) {
	s := setupPauseServer(t)
	var changed atomic.Int32
	s.eventBus.Subscribe(events.EventTasksChanged, func(events.Event) { changed.Add(1) })

	require.NotNil(t, getTaskResponse(t, s, "nightly").NextRunAt)

	assert.Equal(t, http.StatusNoContent, postTask(t, s, "nightly", "pause"))
	paused := getTaskResponse(t, s, "nightly")
	assert.NotNil(t, paused.PausedAt)
	assert.Nil(t, paused.NextRunAt, "a paused task reports no next run")

	// Idempotent: a second pause keeps the original time.
	assert.Equal(t, http.StatusNoContent, postTask(t, s, "nightly", "pause"))
	assert.Equal(t, *paused.PausedAt, *getTaskResponse(t, s, "nightly").PausedAt)

	assert.Equal(t, http.StatusNoContent, postTask(t, s, "nightly", "resume"))
	resumed := getTaskResponse(t, s, "nightly")
	assert.Nil(t, resumed.PausedAt)
	assert.NotNil(t, resumed.NextRunAt)
	assert.Equal(t, http.StatusNoContent, postTask(t, s, "nightly", "resume"), "resume is idempotent")

	assert.Equal(t, int32(4), changed.Load(), "every pause/resume tells dashboards to refetch")
}

func TestPauseHTTP_Rejections(t *testing.T) {
	s := setupPauseServer(t)

	assert.Equal(t, http.StatusNotFound, postTask(t, s, "missing", "pause"))
	assert.Equal(t, http.StatusForbidden, postTask(t, s, "locked", "pause"), "manual_trigger = false locks pausing")
	assert.Equal(t, http.StatusForbidden, postTask(t, s, "locked", "resume"))
	assert.Equal(t, http.StatusConflict, postTask(t, s, "task1", "pause"), "a task without cron has nothing to pause")
}

func TestPauseHTTP_NoSchedulerIsConflict(t *testing.T) {
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) { o.Scheduler = nil })

	assert.Equal(t, http.StatusConflict, postTask(t, s, "task1", "pause"))
	assert.Equal(t, http.StatusConflict, postTask(t, s, "task1", "resume"))
}
