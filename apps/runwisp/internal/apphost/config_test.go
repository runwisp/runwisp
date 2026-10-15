// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apphost

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const digestDoc = `{"tasks":{"digest":{"sdk":true,"cron":"@daily"}}}`

// acceptConfig makes h take config from its active app; the reload loads the
// document the app sent and reports its tasks as added.
func acceptConfig(h *Host, reloadErr error) {
	src := config.NewDocumentSource("/srv/app/runwisp.toml", []byte(`{}`))
	h.AcceptConfig(src, func() (model.ReloadResult, error) {
		if reloadErr != nil {
			return model.ReloadResult{}, reloadErr
		}
		cfg, err := src.Load()
		if err != nil {
			return model.ReloadResult{}, err
		}
		var added []string
		for _, t := range cfg.Tasks {
			added = append(added, t.Name)
		}
		return model.ReloadResult{Added: added}, nil
	})
}

func TestConfigFromTheActiveAppReloads(t *testing.T) {
	h := New()
	acceptConfig(h, nil)
	app := connect(t, h)
	app.role()

	app.send(message{Type: "config", Doc: json.RawMessage(digestDoc)})
	m := app.next()
	require.Equal(t, "config_ok", m.Type, m.Error)
	assert.Equal(t, []string{"digest"}, m.Result.Added)
}

func TestConfigErrors(t *testing.T) {
	restart := fmt.Errorf("[daemon] tls_cert changed; %w", runtime.ErrRestartRequired)
	for name, tc := range map[string]struct {
		reloadErr error
		want      message
	}{
		"invalid": {
			want: message{Type: "config_error", Error: `failed to parse config`},
		},
		"restart": {
			reloadErr: restart,
			want:      message{Type: "config_error", Error: restart.Error(), Restart: true},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := New()
			acceptConfig(h, tc.reloadErr)
			app := connect(t, h)
			app.role()

			app.send(message{Type: "config", Doc: json.RawMessage(`{"tasks":{"digest":{"sdk":true,"bogus":1}}}`)})
			m := app.next()
			assert.Equal(t, tc.want.Type, m.Type)
			assert.Contains(t, m.Error, tc.want.Error)
			assert.Equal(t, tc.want.Restart, m.Restart)
		})
	}
}

func TestConfigFromAStandbyIsRefused(t *testing.T) {
	h := New()
	acceptConfig(h, nil)
	active := connect(t, h)
	active.role()
	standby := connect(t, h)
	standby.role()

	standby.send(message{Type: "config", Doc: json.RawMessage(digestDoc)})
	m := standby.next()
	assert.Equal(t, "config_error", m.Type)
	assert.Contains(t, m.Error, "standby")
}

func TestConfigRefusedWhenTheDaemonReadsTOML(t *testing.T) {
	h := New()
	app := connect(t, h)
	app.role()

	app.send(message{Type: "config", Doc: json.RawMessage(digestDoc)})
	m := app.next()
	assert.Equal(t, "config_error", m.Type)
	assert.Contains(t, m.Error, "runwisp.toml")
}

func TestExitWhenIdle(t *testing.T) {
	h := New()
	timers := make(chan chan time.Time, 4)
	h.after = func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		timers <- c
		return c
	}
	exited := make(chan struct{})
	h.ExitWhenIdle(time.Minute, func() { close(exited) })

	// An app connects before the first idle window ends: no exit.
	first := <-timers
	app := connect(t, h)
	app.role()
	first <- time.Time{}
	select {
	case <-exited:
		t.Fatal("exited with an app connected")
	case <-time.After(50 * time.Millisecond):
	}

	// It leaves and the window runs out: exit.
	require.NoError(t, app.conn.Close())
	(<-timers) <- time.Time{}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("did not exit once idle")
	}
}

func TestExitWhenIdleRightAwayAfterClose(t *testing.T) {
	h := New()
	h.after = func(time.Duration) <-chan time.Time { return nil } // the idle wait never ends
	exited := make(chan struct{})
	h.ExitWhenIdle(time.Minute, func() { close(exited) })

	app := connect(t, h)
	app.role()
	app.send(message{Type: "leave"})
	// Its runs may still be finishing: wait until it disconnects.
	select {
	case <-exited:
		t.Fatal("exited before the last app disconnected")
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, app.conn.Close())
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("did not exit after the last app closed")
	}
}

// A copy that leaves as the last one keeps the daemon up while its runs
// finish, however long they take: the idle window starts once it disconnects.
func TestExitWhenIdleWaitsForALeavingAppsRuns(t *testing.T) {
	h := New()
	idle := make(chan time.Time)
	close(idle)
	h.after = func(d time.Duration) <-chan time.Time {
		if d == time.Minute {
			return idle // the idle window runs out as soon as it starts
		}
		return nil
	}
	app := connect(t, h)
	app.role()
	exited := make(chan struct{})
	h.ExitWhenIdle(time.Minute, func() { close(exited) })
	p := startRun(t, h, app, "r1")

	app.send(message{Type: "leave"})
	select {
	case <-exited:
		t.Fatal("exited while the leaving app's run was still going")
	case <-time.After(50 * time.Millisecond):
	}

	go func() {
		app.send(message{Type: "exit", Run: "r1", Code: code(0)})
		_ = app.conn.Close()
	}()
	_, _, exit := output(t, p)
	assert.Equal(t, 0, exit)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("did not exit after the last app disconnected")
	}
}

// A document a copy sends just before it hands over is applied only if it is
// still active when its turn comes, so the next copy's config wins.
func TestConfigFromACopyThatHandedOverIsRefused(t *testing.T) {
	h := New()
	src := config.NewDocumentSource("/srv/app/runwisp.toml", []byte(`{}`))
	gate := make(chan struct{})
	var calls sync.Mutex
	reloads := 0
	h.AcceptConfig(src, func() (model.ReloadResult, error) {
		calls.Lock()
		reloads++
		first := reloads == 1
		calls.Unlock()
		if first {
			<-gate // the first push is still applying
		}
		return model.ReloadResult{}, nil
	})
	old := connect(t, h)
	old.role()
	next := connect(t, h)
	next.role()

	old.send(message{Type: "config", Doc: json.RawMessage(`{"tasks":{"a":{"sdk":true}}}`)})
	old.send(message{Type: "config", Doc: json.RawMessage(`{"tasks":{"b":{"sdk":true}}}`)})
	old.send(message{Type: "leave"})
	// The close hands over while the first push still applies.
	replies := make(chan message, 3)
	go func() {
		for range 3 {
			replies <- old.next()
		}
	}()
	assert.True(t, next.role())
	close(gate)

	var got []string
	for range 3 {
		m := <-replies
		got = append(got, m.Type)
		if m.Type == "config_error" {
			assert.Contains(t, m.Error, "standby")
		}
	}
	assert.ElementsMatch(t, []string{"role", "config_ok", "config_error"}, got)
}

// A copy that shuts down while another is connected hands over: the other
// becomes active, and the leaving one is told it is on standby.
func TestCloseHandsOverToTheNextCopy(t *testing.T) {
	h := New()
	first := connect(t, h)
	require.True(t, first.role())
	second := connect(t, h)
	require.False(t, second.role())

	first.send(message{Type: "leave"})
	assert.False(t, first.role())
	assert.True(t, second.role())

	started := make(chan struct{})
	go func() {
		_, err := h.Start(context.Background(), &model.Task{Name: "digest"}, &model.Run{ID: "r1"}, digest)
		assert.NoError(t, err)
		close(started)
	}()
	assert.Equal(t, "r1", second.next().Run)
	<-started
}
