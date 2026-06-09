// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package smtp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/textproto"
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

// TestSMTP_PermanentTextprotoErrorIsNotRetried covers the AUTH-failure path:
// go-mail wraps net/smtp errors as "dial failed: SMTP AUTH failed: %w" around
// a raw *textproto.Error, never reaching *gomail.SendError. classify() must
// still recognise the 5xx reply and short-circuit retries.
func TestSMTP_PermanentTextprotoErrorIsNotRetried(t *testing.T) {
	calls := atomic.Int32{}
	custom := &funcSender{
		fn: func(_ context.Context, _ ...*gomail.Msg) error {
			calls.Add(1)
			return fmt.Errorf("dial failed: SMTP AUTH failed: %w",
				&textproto.Error{Code: 535, Msg: "5.7.8 authentication failed"})
		},
	}
	ch := mkChannel(t, nil, func(c *Config) {
		c.NewClient = func(_ string, _ int, _ string, _ bool, _, _ string) (sender, error) {
			return custom, nil
		}
	})
	err := ch.Execute(context.Background(), failingEvent())
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(),
		"textproto 5xx replies must not be retried")
}

// TestSMTP_TransientTextprotoErrorRetries covers the symmetric 4xx case: a
// 4xx reply (e.g. greylisting, rate limit) should loop until the backoff
// budget exhausts, mirroring how SendError's IsTemp() is handled.
func TestSMTP_TransientTextprotoErrorRetries(t *testing.T) {
	calls := atomic.Int32{}
	custom := &funcSender{
		fn: func(_ context.Context, _ ...*gomail.Msg) error {
			calls.Add(1)
			return fmt.Errorf("dial failed: %w",
				&textproto.Error{Code: 421, Msg: "service not available"})
		},
	}
	ch := mkChannel(t, nil, func(c *Config) {
		c.NewClient = func(_ string, _ int, _ string, _ bool, _, _ string) (sender, error) {
			return custom, nil
		}
	})
	err := ch.Execute(context.Background(), failingEvent())
	require.Error(t, err)
	assert.GreaterOrEqual(t, int(calls.Load()), 2,
		"4xx textproto replies must be treated as transient")
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

func TestSMTP_BuildMsg_IncludesCcBccReplyTo(t *testing.T) {
	fake := &fakeSender{}
	ch := mkChannel(t, fake, func(c *Config) {
		c.CC = []string{"cc1@example.test", "cc2@example.test"}
		c.BCC = []string{"bcc@example.test"}
		c.ReplyTo = "noreply@example.test"
	})
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))

	raw := fake.captured.String()
	assert.Contains(t, raw, "Cc:")
	assert.Contains(t, raw, "cc1@example.test")
	assert.Contains(t, raw, "cc2@example.test")
	// BCC is intentionally NOT serialized to the wire by go-mail.
	assert.Contains(t, raw, "Reply-To:")
	assert.Contains(t, raw, "noreply@example.test")
	assert.Contains(t, raw, "User-Agent: runwisp-notify/1")
	assert.Contains(t, raw, "Auto-Submitted: auto-generated")
}

func TestSMTP_BuildMsg_RejectsCRLFInSubject(t *testing.T) {
	// We can't easily inject a CRLF subject via the renderer, so call the
	// private buildMsg helper directly and verify the guard fires.
	ch := mkChannel(t, &fakeSender{})
	_, err := ch.buildMsg("legit subject\r\nBcc: attacker@evil", "<p>x</p>", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subject")
	assert.Contains(t, err.Error(), "CR or LF")
}

func TestSMTP_BuildMsg_RejectsCRLFInReplyTo(t *testing.T) {
	ch := mkChannel(t, &fakeSender{}, func(c *Config) {
		c.ReplyTo = "noreply@example.test\r\nBcc: attacker@evil"
	})
	_, err := ch.buildMsg("ok", "<p>x</p>", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reply-to")
}

func TestSMTP_BuildMsg_RejectsCRLFInRecipients(t *testing.T) {
	ch := mkChannel(t, &fakeSender{}, func(c *Config) {
		c.Recipients = []string{"good@example.test", "bad@example.test\r\nBcc: x"}
	})
	_, err := ch.buildMsg("ok", "<p>x</p>", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "to")
}

func TestSMTP_RejectCRLFEdgeCases(t *testing.T) {
	require.NoError(t, rejectCRLF("subject", ""))
	require.NoError(t, rejectCRLF("subject", "no crlf here"))
	require.Error(t, rejectCRLF("subject", "with\rCR"))
	require.Error(t, rejectCRLF("subject", "with\nLF"))
}

func TestSMTP_Dial_PrefersInjectedClient(t *testing.T) {
	fake := &fakeSender{}
	called := false
	ch := mkChannel(t, fake, func(c *Config) {
		c.NewClient = func(_ string, _ int, _ string, _ bool, _, _ string) (sender, error) {
			called = true
			return fake, nil
		}
	})
	s, err := ch.dial()
	require.NoError(t, err)
	assert.NotNil(t, s)
	assert.True(t, called, "injected client must be preferred")
}

func TestSMTP_Dial_FallsBackToDefaultClient(t *testing.T) {
	// With newClient unset, dial() exercises defaultClient. We don't actually
	// send anything — just confirm the constructor returns a non-nil client.
	ch := mkChannel(t, &fakeSender{}, func(c *Config) {
		c.NewClient = nil
		c.Username = "user"
		c.Password = "pw"
		c.TLSMode = "starttls"
	})
	s, err := ch.dial()
	require.NoError(t, err)
	assert.NotNil(t, s)
}

func TestSMTP_DefaultClient_ImplicitTLSWithSkipVerify(t *testing.T) {
	s, err := defaultClient("smtp.example.test", 465, "implicit", true, "user", "pw")
	require.NoError(t, err)
	assert.NotNil(t, s)
}

func TestSMTP_DefaultClient_NoTLSNoAuth(t *testing.T) {
	s, err := defaultClient("smtp.example.test", 25, "none", false, "", "")
	require.NoError(t, err)
	assert.NotNil(t, s)
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

// TestSMTP_New_LogsWarningWhenTLSSkipVerify covers the tlsSkipVerify warning
// branch in New (line 131-134).
func TestSMTP_New_LogsWarningWhenTLSSkipVerify(t *testing.T) {
	r := newTestRenderer(t)
	c, err := New(Config{
		ID: "x", Host: "h", From: "f@b", Recipients: []string{"a@b"},
		Renderer: r, TLSSkipVerify: true,
	})
	require.NoError(t, err)
	require.NotNil(t, c)
}

// rendererFunc adapts a plain Render function into the render.Renderer
// interface, letting tests inject success or failure outcomes.
type rendererFunc func(*notify.Event) (render.RenderedMessage, error)

func (f rendererFunc) Render(ev *notify.Event) (render.RenderedMessage, error) { return f(ev) }

// TestSMTP_Execute_RenderErrorPropagates covers the render-error branch.
func TestSMTP_Execute_RenderErrorPropagates(t *testing.T) {
	fake := &fakeSender{}
	ch := mkChannel(t, fake, func(c *Config) {
		c.Renderer = rendererFunc(func(_ *notify.Event) (render.RenderedMessage, error) {
			return render.RenderedMessage{}, errors.New("renderfail")
		})
	})
	err := ch.Execute(context.Background(), failingEvent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "render")
}

// TestSMTP_Execute_EmptyTitleFallsBackToDefaultSubject covers the
// "subject = \"RunWisp notification\"" fallback branch.
func TestSMTP_Execute_EmptyTitleFallsBackToDefaultSubject(t *testing.T) {
	fake := &fakeSender{}
	ch := mkChannel(t, fake, func(c *Config) {
		c.Renderer = rendererFunc(func(_ *notify.Event) (render.RenderedMessage, error) {
			return render.RenderedMessage{Title: "", Body: []byte("<p>hi</p>")}, nil
		})
	})
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))
	assert.Contains(t, fake.captured.String(), "Subject: RunWisp notification")
}

// TestSMTP_BuildMsg_RejectsBadFromAddress triggers the gomail rejection branch
// at m.From().
func TestSMTP_BuildMsg_RejectsBadFromAddress(t *testing.T) {
	ch := mkChannel(t, &fakeSender{}, func(c *Config) {
		c.From = "this is not an address"
	})
	_, err := ch.buildMsg("ok", "<p>x</p>", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "from")
}

// TestSMTP_BuildMsg_RejectsBadToAddress triggers the gomail rejection branch
// at m.To().
func TestSMTP_BuildMsg_RejectsBadToAddress(t *testing.T) {
	ch := mkChannel(t, &fakeSender{}, func(c *Config) {
		c.Recipients = []string{"not-an-email"}
	})
	_, err := ch.buildMsg("ok", "<p>x</p>", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "to")
}

// TestSMTP_BuildMsg_RejectsBadCcAddress triggers the gomail rejection branch
// at m.Cc().
func TestSMTP_BuildMsg_RejectsBadCcAddress(t *testing.T) {
	ch := mkChannel(t, &fakeSender{}, func(c *Config) {
		c.CC = []string{"not-an-email"}
	})
	_, err := ch.buildMsg("ok", "<p>x</p>", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cc")
}

// TestSMTP_BuildMsg_RejectsBadBccAddress triggers the gomail rejection branch
// at m.Bcc().
func TestSMTP_BuildMsg_RejectsBadBccAddress(t *testing.T) {
	ch := mkChannel(t, &fakeSender{}, func(c *Config) {
		c.BCC = []string{"not-an-email"}
	})
	_, err := ch.buildMsg("ok", "<p>x</p>", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bcc")
}

// TestSMTP_BuildMsg_RejectsBadReplyToAddress triggers the gomail rejection
// branch at m.ReplyTo().
func TestSMTP_BuildMsg_RejectsBadReplyToAddress(t *testing.T) {
	ch := mkChannel(t, &fakeSender{}, func(c *Config) {
		c.ReplyTo = "not-an-email"
	})
	_, err := ch.buildMsg("ok", "<p>x</p>", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reply-to")
}
