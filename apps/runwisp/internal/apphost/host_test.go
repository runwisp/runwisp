// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apphost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeApp is the app's end of a connection.
type fakeApp struct {
	t    *testing.T
	conn net.Conn
	dec  *json.Decoder
	enc  *json.Encoder
}

func connect(t *testing.T, h *Host) *fakeApp {
	t.Helper()
	return connectAs(t, h, "app", 0)
}

func connectAs(t *testing.T, h *Host, app string, pid int) *fakeApp {
	t.Helper()
	appSide, daemonSide := net.Pipe()
	go h.ServeConn(daemonSide, app, pid)
	t.Cleanup(func() { _ = appSide.Close() })
	return &fakeApp{t: t, conn: appSide, dec: json.NewDecoder(appSide), enc: json.NewEncoder(appSide)}
}

// startRun starts a run of digest and returns its process once the app got it.
func startRun(t *testing.T, h *Host, app *fakeApp, id string) *executor.Process {
	t.Helper()
	started := make(chan *executor.Process, 1)
	go func() {
		p, err := h.Start(context.Background(), &model.Task{Name: "digest"}, &model.Run{ID: id}, digest)
		assert.NoError(t, err)
		started <- p
	}()
	require.Equal(t, id, app.next().Run)
	return <-started
}

// syncBuffer is a bytes.Buffer safe to write from the daemon's goroutines
// while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureSlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func (a *fakeApp) next() message {
	a.t.Helper()
	var m message
	require.NoError(a.t, a.dec.Decode(&m))
	return m
}

func (a *fakeApp) send(m message) {
	a.t.Helper()
	require.NoError(a.t, a.enc.Encode(m))
}

func (a *fakeApp) role() bool {
	a.t.Helper()
	m := a.next()
	require.Equal(a.t, "role", m.Type)
	require.NotNil(a.t, m.Active)
	return *m.Active
}

func code(c int) *int { return &c }

// output drains a process the way the executor does and returns what each
// stream carried and the exit code.
func output(t *testing.T, p *executor.Process) (stdout, stderr string, exit int) {
	t.Helper()
	errCh := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(p.Stderr)
		errCh <- string(b)
	}()
	out, err := io.ReadAll(p.Stdout)
	require.NoError(t, err)
	stderr = <-errCh
	exit, err = p.Wait()
	require.NoError(t, err)
	return string(out), stderr, exit
}

var digest = &model.SDKExecution{}

func TestRunRoundTrip(t *testing.T) {
	h := New()
	app := connect(t, h)
	require.True(t, app.role())

	task := &model.Task{
		Name:       "digest",
		Env:        map[string]string{"REGION": "eu", "TOKEN": "env"},
		Secrets:    map[string]string{"TOKEN": "secret"},
		Parameters: []model.TaskParam{{Key: "DRY_RUN", Kind: model.ParamEnv}},
	}
	run := &model.Run{ID: "r1", Params: map[string]string{"DRY_RUN": "1"}}

	started := make(chan *executor.Process, 1)
	go func() {
		p, err := h.Start(context.Background(), task, run, digest)
		assert.NoError(t, err)
		started <- p
	}()

	m := app.next()
	assert.Equal(t, message{
		Type:   "run",
		Run:    "r1",
		Task:   "digest",
		Env:    map[string]string{"REGION": "eu", "TOKEN": "secret", "DRY_RUN": "1"},
		Params: map[string]string{"DRY_RUN": "1"},
	}, m)
	p := <-started

	go func() {
		app.send(message{Type: "output", Run: "r1", Stream: "stdout", Line: "sent 3 emails"})
		app.send(message{Type: "output", Run: "r1", Stream: "stderr", Line: "1 bounced"})
		app.send(message{Type: "exit", Run: "r1", Code: code(3)})
	}()
	stdout, stderr, exit := output(t, p)
	assert.Equal(t, "sent 3 emails\n", stdout)
	assert.Equal(t, "1 bounced\n", stderr)
	assert.Equal(t, 3, exit)
}

func TestStopAsksTheApp(t *testing.T) {
	h := New()
	app := connect(t, h)
	app.role()

	ctx, cancel := context.WithCancel(context.Background())
	grace := time.Minute
	task := &model.Task{Name: "digest", GracefulStop: &grace}
	started := make(chan *executor.Process, 1)
	go func() {
		p, err := h.Start(ctx, task, &model.Run{ID: "r1"}, digest)
		assert.NoError(t, err)
		started <- p
	}()
	app.next()
	p := <-started

	cancel()
	assert.Equal(t, message{Type: "stop", Run: "r1"}, app.next())
	go app.send(message{Type: "exit", Run: "r1", Code: code(143)})
	_, _, exit := output(t, p)
	assert.Equal(t, 143, exit)
}

func TestStopGivesUpAfterGracefulStop(t *testing.T) {
	h := New()
	expired := make(chan time.Time)
	close(expired)
	h.after = func(time.Duration) <-chan time.Time { return expired }
	app := connect(t, h)
	app.role()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan *executor.Process, 1)
	go func() {
		p, err := h.Start(ctx, &model.Task{Name: "digest"}, &model.Run{ID: "r1"}, digest)
		assert.NoError(t, err)
		started <- p
	}()
	app.next()
	p := <-started

	cancel()
	go func() { app.next() }() // the stop the handler ignores
	_, stderr, exit := output(t, p)
	assert.Contains(t, stderr, "did not stop within graceful_stop")
	assert.Equal(t, -1, exit)
}

func TestDisconnectEndsRuns(t *testing.T) {
	h := New()
	app := connect(t, h)
	app.role()

	started := make(chan *executor.Process, 1)
	go func() {
		p, err := h.Start(context.Background(), &model.Task{Name: "digest"}, &model.Run{ID: "r1"}, digest)
		assert.NoError(t, err)
		started <- p
	}()
	app.next()
	p := <-started

	go func() { _ = app.conn.Close() }()
	_, stderr, exit := output(t, p)
	assert.Contains(t, stderr, "disconnected")
	assert.Equal(t, -1, exit)
}

func TestStandbyTakesOver(t *testing.T) {
	h := New()
	first := connect(t, h)
	require.True(t, first.role())
	second := connect(t, h)
	require.False(t, second.role())

	require.NoError(t, first.conn.Close())
	require.True(t, second.role())

	started := make(chan *executor.Process, 1)
	go func() {
		p, err := h.Start(context.Background(), &model.Task{Name: "digest"}, &model.Run{ID: "r1"}, digest)
		assert.NoError(t, err)
		started <- p
	}()
	assert.Equal(t, "r1", second.next().Run)
	p := <-started
	go second.send(message{Type: "exit", Run: "r1", Code: code(0)})
	_, _, exit := output(t, p)
	assert.Equal(t, 0, exit)
}

func TestRunWaitsForAnAppToConnect(t *testing.T) {
	h := New()
	started := make(chan *executor.Process, 1)
	go func() {
		p, err := h.Start(context.Background(), &model.Task{Name: "digest"}, &model.Run{ID: "r1"}, digest)
		assert.NoError(t, err)
		started <- p
	}()

	app := connect(t, h)
	app.role()
	assert.Equal(t, "r1", app.next().Run)
	p := <-started
	go app.send(message{Type: "exit", Run: "r1", Code: code(0)})
	output(t, p)
}

func TestNoAppFailsTheRun(t *testing.T) {
	h := New()
	expired := make(chan time.Time)
	close(expired)
	h.after = func(time.Duration) <-chan time.Time { return expired }

	_, err := h.Start(context.Background(), &model.Task{Name: "digest"}, &model.Run{ID: "r1"}, digest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no app is connected to run "digest"`)
}

// A run that fires after the last app left, while the daemon shuts down.
func TestNoAppWhileShuttingDownSaysSo(t *testing.T) {
	h := New()
	h.after = func(time.Duration) <-chan time.Time { return nil } // the handoff wait never ends
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := h.Start(ctx, &model.Task{Name: "digest"}, &model.Run{ID: "r1"}, digest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no app is connected to run "digest"`)
}

func TestCloseRefusesApps(t *testing.T) {
	h := New()
	h.Close()
	appSide, daemonSide := net.Pipe()
	h.ServeConn(daemonSide, "app", 0)
	_, err := appSide.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}

// A partial output message continues the line: the next one ends it.
func TestPartialOutputContinuesTheLine(t *testing.T) {
	h := New()
	app := connect(t, h)
	app.role()
	p := startRun(t, h, app, "r1")

	go func() {
		app.send(message{Type: "output", Run: "r1", Stream: "stdout", Line: "50%\r", Partial: true})
		app.send(message{Type: "output", Run: "r1", Stream: "stdout", Line: "100%"})
		app.send(message{Type: "exit", Run: "r1", Code: code(0)})
	}()
	stdout, _, _ := output(t, p)
	assert.Equal(t, "50%\r100%\n", stdout)
}

// A message over maxLine ends the connection, and says so at Warn: its runs
// fail, so the cause must not hide at Debug.
func TestOverlongMessageIsAWarning(t *testing.T) {
	logs := captureSlog(t)
	h := New()
	app := connect(t, h)
	app.role()
	p := startRun(t, h, app, "r1")

	go func() {
		line := `{"type":"output","run":"r1","stream":"stdout","line":"` + strings.Repeat("x", maxLine) + "\"}\n"
		_, _ = io.WriteString(app.conn, line) // fails once the daemon hangs up
	}()
	_, stderr, exit := output(t, p)
	assert.Equal(t, -1, exit)
	assert.Contains(t, stderr, "disconnected")
	assert.Contains(t, logs.String(), "level=WARN")
	assert.Contains(t, logs.String(), "over the size limit")
}

// The active copy's pid reaches a standby, and the active copy hears when it
// is the only one left.
func TestRoleSaysWhoIsActiveAndWhenAlone(t *testing.T) {
	h := New()
	first := connectAs(t, h, "app", 11)
	m := first.next()
	assert.Equal(t, message{Type: "role", Active: new(true), Alone: true}, m)

	second := connectAs(t, h, "app", 22)
	m = second.next()
	assert.Equal(t, message{Type: "role", Active: new(false), Pid: 11}, m)

	require.NoError(t, second.conn.Close())
	assert.Equal(t, message{Type: "alone"}, first.next())
}

// Two different apps on one daemon would swap configs: the second is refused.
func TestADifferentAppIsRefused(t *testing.T) {
	h := New()
	connectAs(t, h, "billing", 11).role()

	req := httptest.NewRequest(http.MethodGet, "/api/local/app?app=shop&pid=22", nil)
	req.Header.Set("Upgrade", Protocol)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `another app ("billing", pid 11)`)
	assert.Contains(t, rec.Body.String(), "own data directory")
}

// An SDK that speaks another protocol version hears which one this daemon speaks.
func TestOtherProtocolVersionIsRefused(t *testing.T) {
	h := New()
	req := httptest.NewRequest(http.MethodGet, "/api/local/app", nil)
	req.Header.Set("Upgrade", "runwisp-app/2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUpgradeRequired, rec.Code)
	assert.Equal(t, "runwisp-app/1", rec.Header().Get("Upgrade"))
}

// A firing on_overlap = "skip" dropped reaches the active app.
func TestSkippedRunReachesTheApp(t *testing.T) {
	bus := events.NewEventBus()
	h := New()
	h.ForwardSkippedFirings(bus)
	app := connect(t, h)
	app.role()

	at := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	skipped := model.ReasonSkipped
	failed := model.ReasonFailed
	bus.Publish(events.EventRunFailed, events.RunEvent{Run: &model.Run{TaskName: "other", EndReason: &failed}})
	bus.Publish(events.EventRunFailed, events.RunEvent{Run: &model.Run{TaskName: "digest", EndReason: &skipped, CreatedAt: at}})
	m := app.next()
	assert.Equal(t, "overlap", m.Type)
	assert.Equal(t, "digest", m.Task)
	require.NotNil(t, m.At)
	assert.True(t, at.Equal(*m.At))
}

// Missed firings are found at boot, before the app that started the daemon
// connects: they wait for it, and only the active copy hears them.
func TestMissedFiringsWaitForTheApp(t *testing.T) {
	bus := events.NewEventBus()
	h := New()
	h.ForwardSkippedFirings(bus)

	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	missed := model.ReasonMissed
	bus.Publish(events.EventRunFailed, events.RunEvent{Run: &model.Run{TaskName: "digest", EndReason: &missed, CreatedAt: at}})

	app := connect(t, h)
	require.NoError(t, app.conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	require.True(t, app.role())
	m := app.next()
	assert.Equal(t, "missed", m.Type)
	assert.Equal(t, "digest", m.Task)
	require.NotNil(t, m.At)
	assert.True(t, at.Equal(*m.At))

	// Delivered once: a second copy gets its role and nothing else.
	standby := connect(t, h)
	require.False(t, standby.role())
	bus.Publish(events.EventRunFailed, events.RunEvent{Run: &model.Run{TaskName: "digest", EndReason: &missed, CreatedAt: at}})
	assert.Equal(t, "missed", app.next().Type)
}

func TestHeldMissedFiringsAreBounded(t *testing.T) {
	bus := events.NewEventBus()
	h := New()
	h.ForwardSkippedFirings(bus)
	missed := model.ReasonMissed
	for range maxHeldMissed + 5 {
		bus.Publish(events.EventRunFailed, events.RunEvent{Run: &model.Run{TaskName: "digest", EndReason: &missed}})
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	assert.Len(t, h.missed, maxHeldMissed)
}

// A run the schedule started tells the app which firing it is; one started by
// hand doesn't.
func TestCronRunCarriesItsFiring(t *testing.T) {
	h := New()
	app := connect(t, h)
	require.True(t, app.role())
	tick := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	for _, run := range []*model.Run{
		{ID: "cron", TriggeredBy: model.TriggeredByCron, CreatedAt: tick},
		{ID: "ui", TriggeredBy: model.TriggeredByUI, CreatedAt: tick},
	} {
		go func() {
			_, err := h.Start(context.Background(), &model.Task{Name: "digest"}, run, digest)
			assert.NoError(t, err)
		}()
		m := app.next()
		require.Equal(t, run.ID, m.Run)
		if run.TriggeredBy == model.TriggeredByCron {
			require.NotNil(t, m.At)
			assert.True(t, tick.Equal(*m.At))
		} else {
			assert.Nil(t, m.At)
		}
		app.send(message{Type: "exit", Run: run.ID, Code: code(0)})
	}
}
