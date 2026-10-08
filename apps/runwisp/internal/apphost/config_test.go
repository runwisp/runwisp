// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apphost

import (
	"context"
	"encoding/json"
	"fmt"
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
	app.send(message{Type: "close"})
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("did not exit after the last app closed")
	}
}

// A copy that shuts down while another is connected hands over: the other
// becomes active, and the leaving one is told it is on standby.
func TestCloseHandsOverToTheNextCopy(t *testing.T) {
	h := New()
	first := connect(t, h)
	require.True(t, first.role())
	second := connect(t, h)
	require.False(t, second.role())

	first.send(message{Type: "close"})
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
