// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"cmp"
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

// grepRuns is what storage's GrepRuns selects, newest start first, but for
// the runs it selects by a pending log archive.
func (m *memRuns) grepRuns(q storage.GrepRunsQuery) []storage.GrepRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	start := func(r *model.Run) time.Time { return *cmp.Or(r.StartedAt, &r.CreatedAt) }
	var runs []*model.Run
	for _, run := range m.runs {
		at := start(run)
		station := run.ExecutionID != nil || slices.Contains(q.ServiceTasks, run.TaskName)
		if station && (len(q.TaskNames) == 0 || slices.Contains(q.TaskNames, run.TaskName)) &&
			(q.Since.IsZero() || !at.Before(q.Since)) && (q.Before.IsZero() || at.Before(q.Before)) {
			runs = append(runs, run)
		}
	}
	slices.SortFunc(runs, func(a, b *model.Run) int { return cmp.Or(start(b).Compare(start(a)), cmp.Compare(b.ID, a.ID)) })
	out := make([]storage.GrepRun, len(runs))
	for i, r := range runs {
		out[i] = storage.GrepRun{ID: r.ID, ExecutionID: r.ExecutionID, TaskName: r.TaskName, CreatedAt: r.CreatedAt}
	}
	return out
}

func (m *memRuns) GrepRuns(_ context.Context, q storage.GrepRunsQuery, yield func(storage.GrepRun) bool) error {
	for _, r := range m.grepRuns(q) {
		if !yield(r) {
			break
		}
	}
	return nil
}

func (m *memRuns) CountGrepRuns(_ context.Context, q storage.GrepRunsQuery) (int, error) {
	return len(m.grepRuns(q)), nil
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
