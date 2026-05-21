// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package smtp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomail "github.com/wneessen/go-mail"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

func newTestRenderer(t *testing.T) render.Renderer {
	t.Helper()
	body, err := render.LoadDefaultTemplate("smtp")
	require.NoError(t, err)
	r, err := render.NewTemplateRenderer("smtp:test", body, "text/html", render.DefaultTitle)
	require.NoError(t, err)
	return r
}

func fastBackoff() notify.BackoffConfig {
	return notify.BackoffConfig{
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     20 * time.Millisecond,
		MaxElapsedTime:  150 * time.Millisecond,
		Multiplier:      2.0,
	}
}

// fakeSender captures every message handed to it. Failure-injection tests
// drive the channel through funcSender instead — keeping this fake passive
// makes the capture-and-assert tests trivial to read.
type fakeSender struct {
	captured bytes.Buffer
}

func (f *fakeSender) DialAndSendWithContext(_ context.Context, messages ...*gomail.Msg) error {
	for _, m := range messages {
		if _, err := m.WriteTo(&f.captured); err != nil {
			return err
		}
	}
	return nil
}

func mkChannel(t *testing.T, fake *fakeSender, opts ...func(*Config)) *Channel {
	t.Helper()
	cfg := Config{
		ID:         "ops",
		Host:       "smtp.example.test",
		Port:       587,
		From:       "RunWisp <runwisp@example.test>",
		Recipients: []string{"alerts@example.test"},
		Renderer:   newTestRenderer(t),
		Backoff:    fastBackoff(),
		NewClient: func(_ string, _ int, _ string, _ bool, _, _ string) (sender, error) {
			return fake, nil
		},
	}
	for _, o := range opts {
		o(&cfg)
	}
	ch, err := New(cfg)
	require.NoError(t, err)
	return ch
}

func failingEvent() *notify.Event {
	return &notify.Event{
		Kind:      notify.KindRunFailed,
		Severity:  notify.SevError,
		TaskName:  "backup-db",
		Reason:    "exit 1",
		Timestamp: time.Now().UTC(),
	}
}

func TestSMTP_SendsMultipartAlternative(t *testing.T) {
	fake := &fakeSender{}
	ch := mkChannel(t, fake)
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))

	raw := fake.captured.String()
	assert.Contains(t, raw, "Subject:", "must include a Subject header")
	assert.Contains(t, raw, "backup-db", "subject/body must mention the task")
	assert.Contains(t, raw, "From:", "must include a From header")
	assert.Contains(t, raw, "To:", "must include a To header")
	assert.Contains(t, raw, "multipart/alternative", "must be a multipart/alternative envelope")
	assert.Contains(t, raw, "text/plain", "must include a text/plain part")
	assert.Contains(t, raw, "text/html", "must include a text/html part")
	assert.Contains(t, raw, "Auto-Submitted: auto-generated", "must mark itself auto-generated (RFC 3834)")
	assert.Contains(t, raw, "X-Auto-Response-Suppress: All", "must suppress vacation responders")
}

func TestSMTP_RetriesTransientThenSucceeds(t *testing.T) {
	calls := atomic.Int32{}
	custom := &funcSender{
		fn: func(_ context.Context, _ ...*gomail.Msg) error {
			if calls.Add(1) == 1 {
				return errors.New("temporary network blip")
			}
			return nil
		},
	}
	ch := mkChannel(t, nil, func(c *Config) {
		c.NewClient = func(_ string, _ int, _ string, _ bool, _, _ string) (sender, error) {
			return custom, nil
		}
	})

	require.NoError(t, ch.Execute(context.Background(), failingEvent()))
	assert.GreaterOrEqual(t, int(calls.Load()), 2, "must retry transient errors")
}

// funcSender is a sender backed by a closure — convenient for per-test
// state without growing fakeSender's surface.
type funcSender struct {
	fn func(ctx context.Context, messages ...*gomail.Msg) error
}

func (s *funcSender) DialAndSendWithContext(ctx context.Context, messages ...*gomail.Msg) error {
	return s.fn(ctx, messages...)
}

func TestSMTP_PermanentSendErrorIsNotRetried(t *testing.T) {
	calls := atomic.Int32{}
	custom := &funcSender{
		fn: func(_ context.Context, _ ...*gomail.Msg) error {
			calls.Add(1)
			return &gomail.SendError{Reason: gomail.ErrSMTPMailFrom}
		},
	}
	ch := mkChannel(t, nil, func(c *Config) {
		c.NewClient = func(_ string, _ int, _ string, _ bool, _, _ string) (sender, error) {
			return custom, nil
		}
	})
	err := ch.Execute(context.Background(), failingEvent())
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(), "permanent send errors must not loop")
}

func TestSMTP_RedactsPasswordInError(t *testing.T) {
	const pw = "s3cretPaSSword"
	custom := &funcSender{
		fn: func(_ context.Context, _ ...*gomail.Msg) error {
			return &gomail.SendError{Reason: gomail.ErrSMTPMailFrom}
		},
	}
	ch := mkChannel(t, nil, func(c *Config) {
		c.Username = "user"
		c.Password = pw
		c.NewClient = func(_ string, _ int, _ string, _ bool, _, _ string) (sender, error) {
			return custom, nil
		}
	})
	// Force a credential-leak shape: inject the password into the error chain.
	custom.fn = func(_ context.Context, _ ...*gomail.Msg) error {
		return errors.New("authentication failed for password " + pw)
	}
	err := ch.Execute(context.Background(), failingEvent())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), pw, "password must never leak into the error")
	assert.Contains(t, err.Error(), "[redacted]")
}

func TestSMTP_NormalizeTLSMode(t *testing.T) {
	cases := []struct {
		mode string
		port int
		want string
	}{
		{"starttls", 587, "starttls"},
		{"implicit", 465, "implicit"},
		{"none", 25, "none"},
		{"", 465, "implicit"},
		{"", 587, "starttls"},
		{"", 25, "starttls"},
		{"  STARTTLS  ", 25, "starttls"},
		{"unknown", 465, "implicit"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, normalizeTLSMode(c.mode, c.port), "mode=%q port=%d", c.mode, c.port)
	}
}

func TestSMTP_HTMLToTextStripsTagsAndDecodesEntities(t *testing.T) {
	in := `<h2>❌ backup-db failed</h2><p>Exited with code <b>1</b> &amp; was killed.</p><pre>line a&#10;line b</pre>`
	out := htmlToText(in)
	assert.Contains(t, out, "backup-db failed")
	assert.Contains(t, out, "Exited with code 1 & was killed.")
	assert.NotContains(t, out, "<")
	assert.NotContains(t, out, ">")
	assert.NotContains(t, out, "&amp;", "entities must be decoded")
}

func TestSMTP_HTMLToText_FallbackMatchesHTMLContent(t *testing.T) {
	fake := &fakeSender{}
	ch := mkChannel(t, fake)
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))
	raw := fake.captured.String()
	// The text/plain part must contain the un-tagged headline text.
	idx := strings.Index(raw, "Content-Type: text/plain")
	require.GreaterOrEqual(t, idx, 0, "must include a text/plain part")
	tail := raw[idx:]
	assert.Contains(t, tail, "backup-db", "plain-text part must mention the task")
}

func TestSMTP_IDAndClose(t *testing.T) {
	ch := mkChannel(t, &fakeSender{})
	assert.Equal(t, "ops", ch.ID())
	assert.NoError(t, ch.Close(context.Background()))
	assert.Equal(t, "smtp:ops", ch.String())
}

func TestSMTP_New_ValidatesConfig(t *testing.T) {
	r := newTestRenderer(t)
	_, err := New(Config{ID: "x", Renderer: r, Recipients: []string{"a@b"}})
	require.Error(t, err, "missing host should fail")

	_, err = New(Config{ID: "x", Host: "h", Renderer: r, Recipients: []string{"a@b"}})
	require.Error(t, err, "missing from should fail")

	_, err = New(Config{ID: "x", Host: "h", From: "f@b", Renderer: r})
	require.Error(t, err, "missing recipients should fail")

	_, err = New(Config{ID: "x", Host: "h", From: "f@b", Recipients: []string{"a@b"}})
	require.Error(t, err, "missing renderer should fail")

	_, err = New(Config{ID: "x", Host: "h", From: "f@b", Recipients: []string{"a@b"}, Renderer: r})
	require.NoError(t, err)
}
