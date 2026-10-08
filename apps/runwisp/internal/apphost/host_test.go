// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apphost

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

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
	appSide, daemonSide := net.Pipe()
	go h.ServeConn(daemonSide)
	t.Cleanup(func() { _ = appSide.Close() })
	return &fakeApp{t: t, conn: appSide, dec: json.NewDecoder(appSide), enc: json.NewEncoder(appSide)}
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

func TestCloseRefusesApps(t *testing.T) {
	h := New()
	h.Close()
	appSide, daemonSide := net.Pipe()
	h.ServeConn(daemonSide)
	_, err := appSide.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}
