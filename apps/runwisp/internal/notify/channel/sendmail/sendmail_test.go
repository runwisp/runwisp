// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package sendmail

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// fixedTime keeps the Date header deterministic. Any constant does; this one is
// readable in a failure message.
var fixedTime = time.Date(2026, 7, 25, 14, 30, 0, 0, time.UTC)

func newTestRenderer(t *testing.T) render.Renderer {
	t.Helper()
	body, err := render.LoadDefaultTemplate("sendmail")
	require.NoError(t, err)
	r, err := render.NewTemplateRenderer("sendmail:test", body, "text/plain", render.DefaultTitle)
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

func failingEvent() *notify.Event {
	return &notify.Event{
		Kind:      notify.KindRunFailed,
		Severity:  notify.SevError,
		TaskName:  "backup-db",
		Reason:    "exit 1",
		Timestamp: fixedTime,
	}
}

// spyRunner records what would have been handed to the MTA and returns a
// scripted sequence of results, so every test can assert on the exact bytes
// without an MTA anywhere near the machine.
type spyRunner struct {
	mu      sync.Mutex
	calls   int
	path    string
	args    []string
	stdin   string
	results []error
}

func (s *spyRunner) run(_ context.Context, path string, args []string, stdin []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path, s.args, s.stdin = path, args, string(stdin)
	s.calls++
	if len(s.results) == 0 {
		return nil
	}
	err := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}
	return err
}

func mkChannel(t *testing.T, spy *spyRunner, tweak ...func(*Config)) *Channel {
	t.Helper()
	cfg := Config{
		ID:         "mta",
		Path:       "/usr/sbin/sendmail",
		From:       "runwisp@example.com",
		Recipients: []string{"ops@example.com"},
		Backoff:    fastBackoff(),
		Renderer:   newTestRenderer(t),
		Now:        func() time.Time { return fixedTime },
		Run:        spy.run,
	}
	for _, fn := range tweak {
		fn(&cfg)
	}
	c, err := New(cfg)
	require.NoError(t, err)
	return c
}

func TestSendmail_PipesAnRFC5322Message(t *testing.T) {
	spy := &spyRunner{}
	ch := mkChannel(t, spy)
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))

	require.Equal(t, 1, spy.calls)
	assert.Equal(t, "/usr/sbin/sendmail", spy.path)

	headers, body, ok := strings.Cut(spy.stdin, "\n\n")
	require.True(t, ok, "message must separate headers from body with a blank line")
	assert.Contains(t, headers, "From: runwisp@example.com")
	assert.Contains(t, headers, "To: ops@example.com")
	assert.Contains(t, headers, "Subject:")
	assert.Contains(t, headers, "Content-Type: text/plain; charset=utf-8")
	assert.Contains(t, body, "backup-db", "the body must say which task")
	assert.True(t, strings.HasSuffix(spy.stdin, "\n"), "a message must end with a newline")
}

// TestSendmail_PassesNoRecipientOnTheCommandLine is the security shape of the
// whole channel: -t makes the MTA read recipients from the headers, so an
// address can never be argv — and so can never be read as a flag.
func TestSendmail_PassesNoRecipientOnTheCommandLine(t *testing.T) {
	spy := &spyRunner{}
	ch := mkChannel(t, spy, func(c *Config) {
		c.Recipients = []string{"-oQ/tmp/evil@example.com"}
	})
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))

	assert.Equal(t, []string{"-t", "-i"}, spy.args)
	for _, arg := range spy.args {
		assert.NotContains(t, arg, "@", "no address may reach the command line")
	}
	assert.Contains(t, spy.stdin, "To: -oQ/tmp/evil@example.com")
}

// TestSendmail_UsesDashIForBodiesWithALoneDot: without -i, a body line that is
// just "." ends the message, silently truncating a job's captured output at
// exactly the point it gets interesting.
func TestSendmail_UsesDashIForBodiesWithALoneDot(t *testing.T) {
	spy := &spyRunner{}
	ch := mkChannel(t, spy)
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))
	assert.Contains(t, spy.args, "-i")
}

func TestSendmail_IncludesCcBccAndReplyTo(t *testing.T) {
	spy := &spyRunner{}
	ch := mkChannel(t, spy, func(c *Config) {
		c.CC = []string{"cc@example.com"}
		c.BCC = []string{"bcc@example.com"}
		c.ReplyTo = "reply@example.com"
	})
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))

	assert.Contains(t, spy.stdin, "Cc: cc@example.com")
	// Bcc rides in the headers because -t is what strips it before delivery;
	// putting it on the command line instead is how a Bcc becomes visible.
	assert.Contains(t, spy.stdin, "Bcc: bcc@example.com")
	assert.Contains(t, spy.stdin, "Reply-To: reply@example.com")
}

// TestSendmail_EncodesANonASCIISubject: a raw UTF-8 subject is not legal in a
// header and arrives as mojibake in most MUAs.
func TestSendmail_EncodesANonASCIISubject(t *testing.T) {
	spy := &spyRunner{}
	ch := mkChannel(t, spy, func(c *Config) {
		c.Renderer = rendererFunc(func(*notify.Event) (render.RenderedMessage, error) {
			return render.RenderedMessage{Title: "zálohovanie zlyhalo", Body: []byte("telo")}, nil
		})
	})
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))

	assert.Contains(t, spy.stdin, "Subject: =?utf-8?q?")
	assert.NotContains(t, spy.stdin, "Subject: zálohovanie")
}

// TestSendmail_RejectsAHeaderWithANewline is the injection guard. A CR or LF in
// a configured value would end its header and let the rest become headers of
// its own — a Bcc that redirects the mail, or a blank line that replaces the
// body.
func TestSendmail_RejectsAHeaderWithANewline(t *testing.T) {
	spy := &spyRunner{}
	ch := mkChannel(t, spy, func(c *Config) {
		c.Renderer = rendererFunc(func(*notify.Event) (render.RenderedMessage, error) {
			return render.RenderedMessage{
				Title: "ok\nBcc: attacker@example.com",
				Body:  []byte("body"),
			}, nil
		})
	})
	err := ch.Execute(context.Background(), failingEvent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CR or LF")
	assert.Zero(t, spy.calls, "nothing may be handed to the MTA once a header is rejected")
}

// TestSendmail_BodyMayContainNewlines is the other half of that guard: the body
// is past the blank line, so a newline in it is just a newline. Rejecting it
// would make the channel useless for the captured output it exists to carry.
func TestSendmail_BodyMayContainNewlines(t *testing.T) {
	spy := &spyRunner{}
	ch := mkChannel(t, spy, func(c *Config) {
		c.Renderer = rendererFunc(func(*notify.Event) (render.RenderedMessage, error) {
			return render.RenderedMessage{Title: "subject", Body: []byte("line one\nline two\n")}, nil
		})
	})
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))
	assert.Contains(t, spy.stdin, "line one\nline two\n")
}

// TestSendmail_RetriesOnTempFail: EX_TEMPFAIL is the MTA saying "the queue is
// busy, ask again", which is the one exit status where giving up loses a
// notification that would have been delivered.
func TestSendmail_RetriesOnTempFail(t *testing.T) {
	spy := &spyRunner{results: []error{
		&ExitError{Code: exTempFail, Stderr: "queue busy"},
		nil,
	}}
	ch := mkChannel(t, spy)
	require.NoError(t, ch.Execute(context.Background(), failingEvent()))
	assert.Equal(t, 2, spy.calls, "EX_TEMPFAIL must be retried")
}

// TestSendmail_DoesNotRetryAPermanentExit: a bad address or a broken MTA config
// fails identically every time, so retrying only delays the failure report.
func TestSendmail_DoesNotRetryAPermanentExit(t *testing.T) {
	spy := &spyRunner{results: []error{&ExitError{Code: 67, Stderr: "unknown user"}}}
	ch := mkChannel(t, spy)

	err := ch.Execute(context.Background(), failingEvent())
	require.Error(t, err)
	assert.Equal(t, 1, spy.calls, "a permanent exit must not be retried")
	assert.Contains(t, err.Error(), "unknown user", "the MTA's own diagnosis is the useful part")
}

func TestExitError_PermanentByCode(t *testing.T) {
	assert.False(t, (&ExitError{Code: exTempFail}).Permanent())
	for _, code := range []int{1, 64, 67, 68, 69, 70, 77, 78} {
		assert.True(t, (&ExitError{Code: code}).Permanent(), "exit %d should be permanent", code)
	}
}

// TestSendmail_ResolvesTheSystemBinary walks the well-known locations in order
// and falls back to $PATH, so an operator with a working MTA configures nothing.
func TestSendmail_ResolvesTheSystemBinary(t *testing.T) {
	cases := []struct {
		name    string
		present map[string]bool
		lookup  map[string]string
		want    string
	}{
		{
			name:    "prefers the first well-known location",
			present: map[string]bool{"/usr/sbin/sendmail": true, "/usr/bin/sendmail": true},
			want:    "/usr/sbin/sendmail",
		},
		{
			name:    "falls through to a later one",
			present: map[string]bool{"/usr/bin/sendmail": true},
			want:    "/usr/bin/sendmail",
		},
		{
			name:   "falls back to $PATH",
			lookup: map[string]string{"sendmail": "/opt/homebrew/bin/sendmail"},
			want:   "/opt/homebrew/bin/sendmail",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyRunner{}
			ch := mkChannel(t, spy, func(c *Config) {
				c.Path = ""
				c.LookPath = func(name string) (string, error) {
					if tc.present[name] {
						return name, nil
					}
					if resolved, ok := tc.lookup[name]; ok {
						return resolved, nil
					}
					return "", errors.New("not found")
				}
			})
			require.NoError(t, ch.Execute(context.Background(), failingEvent()))
			assert.Equal(t, tc.want, spy.path)
		})
	}
}

// TestSendmail_NoMTAIsAPermanentErrorThatSaysWhatToDo. The daemon runs fine
// without an MTA, so this surfaces as a delivery failure rather than a refusal
// to boot — which makes the message the operator's only clue.
func TestSendmail_NoMTAIsAPermanentErrorThatSaysWhatToDo(t *testing.T) {
	spy := &spyRunner{}
	ch := mkChannel(t, spy, func(c *Config) {
		c.Path = ""
		c.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	})

	err := ch.Execute(context.Background(), failingEvent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/usr/sbin/sendmail", "name where it looked")
	assert.Contains(t, err.Error(), "sendmail_path", "name the key that overrides it")
	assert.Zero(t, spy.calls)
}

func TestSendmail_RequiresFromAndRecipients(t *testing.T) {
	base := func() Config {
		return Config{ID: "mta", From: "a@example.com", Recipients: []string{"b@example.com"}, Renderer: newTestRenderer(t)}
	}
	cfg := base()
	cfg.From = "  "
	_, err := New(cfg)
	assert.ErrorContains(t, err, "from is required")

	cfg = base()
	cfg.Recipients = nil
	_, err = New(cfg)
	assert.ErrorContains(t, err, "recipient")

	cfg = base()
	cfg.Renderer = nil
	_, err = New(cfg)
	assert.ErrorContains(t, err, "renderer")
}

func TestSendmail_HonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	spy := &spyRunner{results: []error{&ExitError{Code: exTempFail}}}
	ch := mkChannel(t, spy)
	err := ch.Execute(ctx, failingEvent())
	require.Error(t, err)
}

func TestTrimStderr(t *testing.T) {
	assert.Equal(t, "boom", trimStderr("  boom\n"))
	long := trimStderr(strings.Repeat("x", maxStderr+500))
	assert.Equal(t, maxStderr+len("…"), len(long))
	assert.True(t, strings.HasSuffix(long, "…"))
}

type rendererFunc func(*notify.Event) (render.RenderedMessage, error)

func (f rendererFunc) Render(ev *notify.Event) (render.RenderedMessage, error) { return f(ev) }
