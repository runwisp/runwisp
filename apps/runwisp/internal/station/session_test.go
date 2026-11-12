// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/stationtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSession_HelloDescribesTheDaemon(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.run(t)

	hello := stationtest.Decode[protocol.Hello](t, st, "hello", nil)
	assert.Equal(t, []string{"Bearer tok"}, st.Tokens())
	assert.Equal(t, "fp-1", hello.InstanceID)
	assert.Equal(t, "1.2.3", hello.Agent.Version)
	assert.NotEmpty(t, hello.Agent.Os)
	assert.Equal(t, map[string]string{}, hello.Labels)
	assert.Equal(t, []protocol.InventoryEntry{}, hello.Executions)
	assert.NotEmpty(t, hello.TasksHash)
	assert.Equal(t, &protocol.FeatureStatus{Available: true}, hello.Features["exec.shell"])
	assert.False(t, hello.Features["exec.container"].Available)
	assert.False(t, hello.Features["agent.restart"].Available)
	assert.False(t, hello.Features["agent.upgrade"].Available)
	assert.Len(t, hello.Features, 7)
}

func TestSession_StationWithoutV1IsTooOld(t *testing.T) {
	st := stationtest.New(t)
	st.NoSubprotocol = true
	h := newHarness(t, st, nil)

	_, err := h.client.connectOnce(t.Context())
	require.ErrorIs(t, err, errStationTooOld)
	assert.Zero(t, st.Count("hello"), "nothing is sent to a station that doesn't speak v1")
}

func TestSession_UnknownCommandIsUnsupported(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.start(t)

	// A non-command type the daemon doesn't know is ignored, and the session
	// carries on.
	st.Send(t, "future:notice", "", map[string]any{"x": 1})
	id := st.Command(t, "future:command", map[string]any{})
	res := st.Result(t, id, nil)
	assert.False(t, res.Ok)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeUnsupported, res.Error.Code)
	assert.False(t, res.Error.Retryable)

	id = st.Command(t, "session:update", protocol.SessionUpdate{})
	assert.True(t, st.Result(t, id, nil).Ok)
}

func TestSession_RequiresMissingFeatureIsUnsupported(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.start(t)

	dispatch := protocol.ExecutionDispatch{ExecutionID: "e1", Spec: []byte(`{"type":"container","script":"echo hi"}`)}
	for _, feature := range []string{"exec.container", "exec.teleport"} {
		id := st.Command(t, "execution:dispatch", dispatch, feature)
		res := st.Result(t, id, nil)
		require.NotNil(t, res.Error, feature)
		assert.Equal(t, protocol.ErrorCodeUnsupported, res.Error.Code, feature)
		assert.Contains(t, res.Error.Message, feature)
	}
	assert.Empty(t, h.runner.triggered, "a refused command does nothing")
}

func TestSession_DispatchAcceptedThenExpired(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.runner.trigRun = &model.Run{ID: "r1"}
	h.start(t)

	spec := []byte(`{"type":"shell","script":"echo hi"}`)
	id := st.Command(t, "execution:dispatch", protocol.ExecutionDispatch{ExecutionID: "e1", Task: &protocol.TaskRef{ID: "t1"}, Spec: spec}, "exec.shell")
	assert.True(t, st.Result(t, id, nil).Ok)
	assert.Equal(t, []string{"e1"}, h.runner.triggeredIDs())

	// A re-sent dispatch is accepted again without a second run.
	id = st.Command(t, "execution:dispatch", protocol.ExecutionDispatch{ExecutionID: "e1", Spec: spec})
	assert.True(t, st.Result(t, id, nil).Ok)
	assert.Equal(t, []string{"e1"}, h.runner.triggeredIDs())

	past := time.Now().Add(-time.Minute)
	id = st.Command(t, "execution:dispatch", protocol.ExecutionDispatch{ExecutionID: "e2", Spec: spec, NotAfter: &past})
	res := st.Result(t, id, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeExpired, res.Error.Code)
	assert.Equal(t, []string{"e1"}, h.runner.triggeredIDs())
}

func TestSession_TasksSyncWhenStationLacksTheCatalog(t *testing.T) {
	st := stationtest.New(t)
	st.Welcome.TasksHashKnown = false
	h := newHarness(t, st, nil)
	h.start(t)

	sync := stationtest.Decode[protocol.TasksSync](t, st, "tasks:sync", nil)
	assert.NotEmpty(t, sync.Hash)
	assert.Equal(t, []protocol.TaskDefinition{}, sync.Tasks)
}

func TestSession_ReloadSendsAgentUpdateAndTasksSync(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	registry := runtime.NewTaskRegistry(nil)
	h.client.deps.LocalTasks = registry
	h.client.handler.localTasks = registry
	h.start(t)

	registry.Set(&model.Task{Name: "backup", Run: "echo backup", ManualTrigger: true})
	h.bus.Publish(events.EventTasksChanged, nil)

	update := stationtest.Decode[protocol.AgentUpdate](t, st, "agent:update", nil)
	assert.True(t, update.Features["exec.config"].Available, "the first TOML task makes config dispatch available")
	sync := stationtest.Decode[protocol.TasksSync](t, st, "tasks:sync", nil)
	require.Len(t, sync.Tasks, 1)
	assert.Equal(t, "backup", sync.Tasks[0].Name)
}

func TestSession_ServiceStatusAfterWelcome(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.runner.tasks["web"] = &model.Task{Name: "web", Kind: model.KindService, Run: "serve"}
	h.runner.snapshot = model.ServiceSnapshot{TaskName: "web", State: model.ServiceRunning, DesiredInstances: 1, RunningInstances: 1}
	h.runner.snapshotOK = true
	h.start(t)

	status := stationtest.Decode[protocol.ServiceStatus](t, st, "service:status", nil)
	assert.Equal(t, "web", status.TaskID)
	assert.Equal(t, protocol.ServiceStateRunning, status.State)
	assert.Equal(t, []protocol.ServiceInstance{}, status.Instances)
}

func TestSession_StatsFollowSessionUpdate(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.client.deps.SystemStats = func() model.SystemStats { return model.SystemStats{CPUCores: 4} }
	h.start(t)

	id := st.Command(t, "session:update", protocol.SessionUpdate{StatsIntervalMs: new(10)})
	require.True(t, st.Result(t, id, nil).Ok)
	stats := stationtest.Decode[protocol.AgentStats](t, st, "agent:stats", nil)
	assert.Equal(t, 4, stats.CpuCores)
}

// The outbox survives a daemon restart: an update the station never acked is
// reported in hello and sent again with the same seq, and seqs keep growing
// after the acked rows are gone.
func TestOutbox_ResentAfterRestartUntilAcked(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "runwisp.db")
	db := openDB(t, dbPath)

	st := stationtest.New(t)
	h := newHarness(t, st, db)
	h.start(t)
	publishRun(h.bus, events.EventRunCompleted, finishedRun("e1", "t"))
	first := stationtest.Decode[protocol.ExecutionUpdate](t, st, "execution:update", nil)
	assert.Equal(t, int64(1), first.Seq)
	assert.Equal(t, protocol.ExecutionStatusFinished, first.Status)
	h.stop()
	require.NoError(t, db.Close())

	// The daemon restarts on the same database; the station acks this time.
	db = openDB(t, dbPath)
	st2 := stationtest.New(t)
	st2.AutoAck = true
	h2 := newHarness(t, st2, db)
	h2.run(t)
	hello := stationtest.Decode[protocol.Hello](t, st2, "hello", nil)
	assert.Equal(t, int64(1), hello.LastSeq)
	assert.Equal(t, []protocol.InventoryEntry{{ExecutionID: "e1", Status: protocol.ExecutionStatusFinished}}, hello.Executions)

	again := stationtest.Decode[protocol.ExecutionUpdate](t, st2, "execution:update", nil)
	assert.Equal(t, first, again)
	require.Eventually(t, func() bool {
		rows, err := db.ListOutboxUpdates(context.Background(), 0, 10)
		return err == nil && len(rows) == 0
	}, stationtest.WaitTimeout, 10*time.Millisecond, "the ack deletes the update")

	publishRun(h2.bus, events.EventRunCompleted, finishedRun("e2", "t"))
	next := stationtest.Decode[protocol.ExecutionUpdate](t, st2, "execution:update", func(u protocol.ExecutionUpdate) bool { return u.ExecutionID == "e2" })
	assert.Equal(t, int64(2), next.Seq)
}

func TestGoodbye_NoRetryStopsTheIntegration(t *testing.T) {
	st := stationtest.New(t)
	st.Goodbye = &protocol.Goodbye{Code: protocol.GoodbyeCodeRevoked, Message: "token revoked", Retry: false}
	h := newHarness(t, st, nil)

	done := make(chan error, 1)
	go func() { done <- h.client.Run(t.Context()) }()
	select {
	case err := <-done:
		g, ok := errors.AsType[*goodbyeError](err)
		require.True(t, ok, "Run returns the goodbye: %v", err)
		assert.Equal(t, protocol.GoodbyeCodeRevoked, g.Code)
	case <-time.After(stationtest.WaitTimeout):
		t.Fatal("Run kept going after goodbye{retry:false}")
	}
	// The station records hello after reacting to it: wait for the record.
	st.Wait(t, "hello", nil)
	assert.Equal(t, 1, st.Count("hello"), "no reconnect")
}

func TestGoodbye_Delay(t *testing.T) {
	for range 50 {
		d := (&goodbyeError{protocol.Goodbye{Code: protocol.GoodbyeCodeDraining, Retry: true, RetryAfterMs: 1000}}).delay()
		assert.True(t, d >= 0 && d <= time.Second, "draining spreads reconnects over [0, retryAfterMs]: %s", d)

		d = (&goodbyeError{protocol.Goodbye{Code: protocol.GoodbyeCodeDuplicateInstance, Retry: true, RetryAfterMs: 1000}}).delay()
		assert.True(t, d >= time.Second && d <= 1200*time.Millisecond, "other codes wait at least retryAfterMs: %s", d)
	}
	assert.Zero(t, (&goodbyeError{protocol.Goodbye{Code: "brand_new_code", Retry: true}}).delay(), "no retryAfterMs: the normal backoff applies")
}

func TestAgentRestart_ReplyThenRestart(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	restarted := make(chan struct{})
	h.client.deps.RequestRestart = func() error { close(restarted); return nil }
	var allowed atomic.Bool
	h.client.deps.CanRestart = func() error {
		if !allowed.Load() {
			return errors.New("not under a service manager")
		}
		return nil
	}
	h.start(t)

	id := st.Command(t, "agent:restart", struct{}{})
	res := st.Result(t, id, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeForbidden, res.Error.Code)
	assert.Contains(t, res.Error.Message, "service manager")

	allowed.Store(true)
	id = st.Command(t, "agent:restart", struct{}{}, "agent.restart")
	assert.True(t, st.Result(t, id, nil).Ok)
	select {
	case <-restarted:
	case <-time.After(stationtest.WaitTimeout):
		t.Fatal("no restart after the ok")
	}
}

func TestAgentUpgrade(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	installed := make(chan string, 1)
	restarted := make(chan struct{})
	h.client.deps.Upgrade = func(_ context.Context, tag string) (func(), error) {
		installed <- tag
		return func() { close(restarted) }, nil
	}
	var optedIn atomic.Bool
	h.client.deps.CanUpgrade = func() error {
		if !optedIn.Load() {
			return errors.New("station upgrades disabled")
		}
		return nil
	}
	h.start(t)

	id := st.Command(t, "agent:upgrade", protocol.AgentUpgrade{Version: "v1.3.0"})
	res := st.Result(t, id, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeForbidden, res.Error.Code)

	optedIn.Store(true)
	id = st.Command(t, "agent:upgrade", protocol.AgentUpgrade{Version: "main"})
	res = st.Result(t, id, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeInvalid, res.Error.Code)

	id = st.Command(t, "agent:upgrade", protocol.AgentUpgrade{Version: "1.3.0"}, "agent.upgrade")
	assert.True(t, st.Result(t, id, nil).Ok)
	select {
	case <-restarted:
	case <-time.After(stationtest.WaitTimeout):
		t.Fatal("no restart after the upgrade")
	}
	assert.Equal(t, "v1.3.0", <-installed)
}

func TestAgentCredentials_RotatedTokenIsDurableAndUsed(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.start(t)

	id := st.Command(t, "agent:credentials", protocol.AgentCredentials{Token: "rt_new"})
	require.True(t, st.Result(t, id, nil).Ok)

	// Stored before the ok: a restart with the same configured token uses it.
	cfg := Config{Enabled: true, StationToken: "tok", ConfiguredToken: "tok"}
	require.NoError(t, ApplyRotatedToken(t.Context(), h.db, &cfg))
	assert.Equal(t, "rt_new", cfg.StationToken)
	// A newly configured token wins over the stored rotation.
	cfg = Config{Enabled: true, StationToken: "rt_fresh", ConfiguredToken: "rt_fresh"}
	require.NoError(t, ApplyRotatedToken(t.Context(), h.db, &cfg))
	assert.Equal(t, "rt_fresh", cfg.StationToken)

	// The next connection dials with the new token.
	st.Drop()
	require.Eventually(t, func() bool { return len(st.Tokens()) == 2 }, stationtest.WaitTimeout, 10*time.Millisecond)
	assert.Equal(t, "Bearer rt_new", st.Tokens()[1])
}

func TestAgentCredentials_RefusedWithoutAStore(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.client.deps.Credentials = nil
	h.start(t)

	id := st.Command(t, "agent:credentials", protocol.AgentCredentials{Token: "rt_new"})
	res := st.Result(t, id, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeForbidden, res.Error.Code)
}

func TestMissingFeatures(t *testing.T) {
	h := newHarness(t, stationtest.New(t), nil)
	h.client.handler.availability = executor.Availability{Shell: executor.BackendStatus{Available: true}}
	assert.Empty(t, h.client.missingFeatures(nil))
	assert.Empty(t, h.client.missingFeatures([]string{"exec.shell"}))
	assert.Contains(t, h.client.missingFeatures([]string{"exec.shell", "exec.http"}), "exec.http")
	assert.Contains(t, h.client.missingFeatures([]string{"exec.future"}), "not supported")
}

func TestGoodbye_SentOnShutdown(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.start(t)

	h.stop()
	bye := stationtest.Decode[protocol.Goodbye](t, st, "goodbye", nil)
	assert.Equal(t, protocol.Goodbye{Code: protocol.GoodbyeCodeShutdown, Message: "daemon shutting down", Retry: true}, bye)
}
