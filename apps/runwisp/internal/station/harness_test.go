// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/stationtest"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/stretchr/testify/require"
)

// memRuns is a run store keyed by run id.
type memRuns struct {
	mu   sync.Mutex
	runs map[string]*model.Run
}

func (m *memRuns) put(run *model.Run) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		m.runs = map[string]*model.Run{}
	}
	m.runs[run.ID] = run
}

func (m *memRuns) find(match func(*model.Run) bool) (*model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, run := range m.runs {
		if match(run) {
			copied := *run
			return &copied, nil
		}
	}
	return nil, storage.ErrNotFound
}

func (m *memRuns) GetRunByExecutionID(_ context.Context, id string) (*model.Run, error) {
	return m.find(func(r *model.Run) bool { return r.ExecutionID != nil && *r.ExecutionID == id })
}

func (m *memRuns) GetRun(_ context.Context, id string) (*model.Run, error) {
	return m.find(func(r *model.Run) bool { return r.ID == id })
}

// QueryRuns honours the task name filter and paging, newest first.
func (m *memRuns) QueryRuns(_ context.Context, q storage.RunQuery) ([]model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Run
	for _, run := range m.runs {
		if q.Filter.TaskName == "" || run.TaskName == q.Filter.TaskName {
			out = append(out, *run)
		}
	}
	slices.SortFunc(out, func(a, b model.Run) int { return b.CreatedAt.Compare(a.CreatedAt) })
	out = out[min(q.Offset, len(out)):]
	return out[:min(q.Limit, len(out))], nil
}

// harness is a station client wired to a scripted station, an in-memory run
// store and a real SQLite outbox.
type harness struct {
	st     *stationtest.Station
	client *Client
	db     *storage.SQLiteDatabase
	runs   *memRuns
	runner *fakeTaskRunner
	bus    *events.Bus
	logDir string
	done   chan error
	cancel context.CancelFunc
}

func openDB(t *testing.T, path string) *storage.SQLiteDatabase {
	t.Helper()
	db, err := storage.New(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newHarness builds a client against st; edit h.client.deps before start.
func newHarness(t *testing.T, st *stationtest.Station, db *storage.SQLiteDatabase) *harness {
	t.Helper()
	if db == nil {
		db = openDB(t, filepath.Join(t.TempDir(), "runwisp.db"))
	}
	h := &harness{
		st:     st,
		db:     db,
		runs:   &memRuns{},
		runner: &fakeTaskRunner{tasks: map[string]*model.Task{}},
		bus:    events.NewEventBus(),
		logDir: t.TempDir(),
	}
	cfg := Config{
		Enabled:         true,
		BaseURL:         st.URL,
		StationToken:    "tok",
		ConfiguredToken: "tok",
		AgentVersion:    "1.2.3",
		Fingerprint:     "fp-1",
		AllowInsecure:   true, // uploads go to a loopback test server
	}
	client, err := NewClient(cfg, Dependencies{
		TaskManager:       h.runner,
		RunRepo:           h.runs,
		PendingUploadRepo: db,
		Outbox:            db,
		Credentials:       db,
		EventBus:          h.bus,
		LogDir:            h.logDir,
		Availability:      executor.Availability{Shell: executor.BackendStatus{Available: true}},
	})
	require.NoError(t, err)
	h.client = client
	return h
}

// run runs the client until the test ends.
func (h *harness) run(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	go func() { h.done <- h.client.Run(ctx) }()
	t.Cleanup(h.stop)
}

// start runs the client and waits until its session is up.
func (h *harness) start(t *testing.T) {
	t.Helper()
	h.run(t)
	h.st.Wait(t, "hello", nil)
	require.Eventually(t, func() bool { return h.client.current() != nil }, stationtest.WaitTimeout, time.Millisecond)
}

// stop ends the client and waits for Run to return.
func (h *harness) stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(stationtest.WaitTimeout):
	}
	h.cancel = nil
}

// finishedRun is a station run that ended successfully.
func finishedRun(executionID, task string) *model.Run {
	started := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	ended := started.Add(time.Second)
	reason := model.ReasonSuccess
	return &model.Run{
		ID:          "run-" + executionID,
		TaskName:    task,
		ExecutionID: new(executionID),
		Status:      model.PhaseEnded,
		EndReason:   &reason,
		CreatedAt:   started,
		StartedAt:   &started,
		EndedAt:     &ended,
	}
}

func publishRun(bus *events.Bus, typ events.EventType, run *model.Run) {
	bus.Publish(typ, events.RunEvent{Run: run})
}
