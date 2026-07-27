// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package smtp implements the email (SMTP) notify channel. It assembles a
// multipart/alternative message — HTML rendered from the embedded template,
// plus a plain-text alternative auto-derived from the HTML — and delivers it
// via a transient connection per send. Connection reuse is intentionally
// skipped: notification volume is tiny, per-send dials simplify TLS-mode
// switching, and a fresh dial recovers cleanly from transient relay restarts.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/textproto"
	"regexp"
	"strings"

	"github.com/cenkalti/backoff/v4"
	gomail "github.com/wneessen/go-mail"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

const (
	tlsModeSTARTTLS = "starttls"
	tlsModeImplicit = "implicit"
	tlsModeNone     = "none"
)

// Channel is an SMTP notifier. The struct stores resolved config + a hook for
// constructing the go-mail client (overridable in tests to point at a fake
// dialer).
type Channel struct {
	id            string
	host          string
	port          int
	tlsMode       string
	tlsSkipVerify bool
	username      string
	password      string
	from          string
	replyTo       string
	to            []string
	cc            []string
	bcc           []string
	backoff       notify.BackoffConfig
	renderer      render.Renderer
	// newClient is the dial-and-send seam. Tests substitute a stub that hits a
	// local listener; production callers leave this nil and the channel uses
	// the default go-mail client.
	newClient func() (sender, error)
}

// Config is the inputs the factory needs to build an SMTP Channel.
type Config struct {
	ID            string
	Host          string
	Port          int
	TLSMode       string // "starttls" | "implicit" | "none" | "" (port-derived)
	TLSSkipVerify bool
	Username      string
	Password      string
	From          string
	ReplyTo       string
	Recipients    []string
	CC            []string
	BCC           []string
	Backoff       notify.BackoffConfig
	Renderer      render.Renderer
	// NewClient lets tests inject a fake go-mail client. Nil means use the
	// default constructor.
	NewClient func(host string, port int, tlsMode string, tlsSkipVerify bool, username, password string) (sender, error)
}

// sender is the subset of *gomail.Client the channel actually uses. Defining
// it explicitly lets unit tests substitute a fake without standing up a real
// SMTP server.
type sender interface {
	DialAndSendWithContext(ctx context.Context, messages ...*gomail.Msg) error
}

// New constructs an SMTP channel and validates the static configuration. The
// dial happens later, on Execute — startup-time errors are restricted to
// config-shape problems the loader is supposed to catch.
func New(cfg Config) (*Channel, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("smtp channel %q: host is required", cfg.ID)
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("smtp channel %q: from is required", cfg.ID)
	}
	if len(cfg.Recipients) == 0 {
		return nil, fmt.Errorf("smtp channel %q: at least one recipient is required", cfg.ID)
	}
	if cfg.Renderer == nil {
		return nil, fmt.Errorf("smtp channel %q: renderer is required", cfg.ID)
	}
	mode := normalizeTLSMode(cfg.TLSMode, cfg.Port)
	bo := cfg.Backoff
	if bo.IsZero() {
		bo = notify.DefaultBackoff()
	}

	c := &Channel{
		id:            cfg.ID,
		host:          strings.TrimSpace(cfg.Host),
		port:          cfg.Port,
		tlsMode:       mode,
		tlsSkipVerify: cfg.TLSSkipVerify,
		username:      cfg.Username,
		password:      cfg.Password,
		from:          cfg.From,
		replyTo:       cfg.ReplyTo,
		to:            append([]string(nil), cfg.Recipients...),
		cc:            append([]string(nil), cfg.CC...),
		bcc:           append([]string(nil), cfg.BCC...),
		backoff:       bo,
		renderer:      cfg.Renderer,
	}
	if cfg.NewClient != nil {
		c.newClient = func() (sender, error) {
			return cfg.NewClient(c.host, c.port, c.tlsMode, c.tlsSkipVerify, c.username, c.password)
		}
	}
	if c.tlsSkipVerify {
		slog.Warn("smtp channel skipping TLS certificate verification — acceptable only in operator-controlled environments",
			"channel", c.id, "host", c.host, "port", c.port)
	}
	return c, nil
}

func (c *Channel) ID() string                  { return c.id }
func (c *Channel) Close(context.Context) error { return nil }
func (c *Channel) String() string              { return "smtp:" + c.id }

// Execute renders the event, assembles a multipart/alternative message, and
// dials the relay. Retries are gated by SendError.IsTemp() — 4xx temporary
// codes loop; 5xx and config-shape errors are returned as permanent.
func (c *Channel) Execute(ctx context.Context, ev *notify.Event) error {
	rendered, err := c.renderer.Render(ev)
	if err != nil {
		return fmt.Errorf("%s: render: %w", c, err)
	}
	htmlBody := string(rendered.Body)
	textBody := htmlToText(htmlBody)
	subject := strings.TrimSpace(rendered.Title)
	if subject == "" {
		subject = "RunWisp notification"
	}

	msg, err := c.buildMsg(subject, htmlBody, textBody)
	if err != nil {
		return fmt.Errorf("%s: build message: %w", c, err)
	}

	op := func() error {
		client, err := c.dial()
		if err != nil {
			return err
		}
		if err := client.DialAndSendWithContext(ctx, msg); err != nil {
			return c.classify(err)
		}
		return nil
	}

	if err := notify.RetryWithBackoff(ctx, c.backoff, op); err != nil {
		return fmt.Errorf("%s: %s", c, notify.Redact(err.Error(), c.password))
	}
	return nil
}

func (c *Channel) buildMsg(subject, htmlBody, textBody string) (*gomail.Msg, error) {
	if err := c.rejectHeaderCRLF(subject); err != nil {
		return nil, err
	}
	m := gomail.NewMsg()
	if err := c.setAddresses(m); err != nil {
		return nil, err
	}
	m.Subject(subject)
	// Plain text first; HTML supplied as the preferred alternative. RFC 2046
	// requires the most preferred alternative last in multipart/alternative.
	m.SetBodyString(gomail.TypeTextPlain, textBody)
	m.AddAlternativeString(gomail.TypeTextHTML, htmlBody)
	m.SetGenHeader(gomail.HeaderUserAgent, "runwisp-notify/1")
	m.SetGenHeader("Auto-Submitted", "auto-generated")
	m.SetGenHeader("X-Auto-Response-Suppress", "All")
	return m, nil
}

// rejectHeaderCRLF is defense-in-depth: reject CRLF in any header-bound value
// before handing to go-mail. Addresses come from TOML (trusted) and the subject
// from the rendered template; this guard catches a future code path that lets
// untrusted text reach a header.
func (c *Channel) rejectHeaderCRLF(subject string) error {
	if err := rejectCRLF("subject", subject); err != nil {
		return err
	}
	if err := rejectCRLF("from", c.from); err != nil {
		return err
	}
	for _, addr := range c.to {
		if err := rejectCRLF("to", addr); err != nil {
			return err
		}
	}
	for _, addr := range c.cc {
		if err := rejectCRLF("cc", addr); err != nil {
			return err
		}
	}
	for _, addr := range c.bcc {
		if err := rejectCRLF("bcc", addr); err != nil {
			return err
		}
	}
	return rejectCRLF("reply-to", c.replyTo)
}

// setAddresses populates the From/To/Cc/Bcc/Reply-To headers on m.
func (c *Channel) setAddresses(m *gomail.Msg) error {
	if err := m.From(c.from); err != nil {
		return fmt.Errorf("from %q: %w", c.from, err)
	}
	if err := m.To(c.to...); err != nil {
		return fmt.Errorf("to %v: %w", c.to, err)
	}
	if len(c.cc) > 0 {
		if err := m.Cc(c.cc...); err != nil {
			return fmt.Errorf("cc %v: %w", c.cc, err)
		}
	}
	if len(c.bcc) > 0 {
		if err := m.Bcc(c.bcc...); err != nil {
			return fmt.Errorf("bcc %v: %w", c.bcc, err)
		}
	}
	if c.replyTo != "" {
		if err := m.ReplyTo(c.replyTo); err != nil {
			return fmt.Errorf("reply-to %q: %w", c.replyTo, err)
		}
	}
	return nil
}

func (c *Channel) dial() (sender, error) {
	if c.newClient != nil {
		return c.newClient()
	}
	return defaultClient(c.host, c.port, c.tlsMode, c.tlsSkipVerify, c.username, c.password)
}

func defaultClient(host string, port int, tlsMode string, tlsSkipVerify bool, username, password string) (sender, error) {
	opts := []gomail.Option{}
	if port > 0 {
		opts = append(opts, gomail.WithPort(port))
	}
	switch tlsMode {
	case tlsModeImplicit:
		opts = append(opts, gomail.WithSSL())
	case tlsModeNone:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	default: // starttls
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	if tlsSkipVerify {
		// Acceptable in operator-controlled environments only; surfaced via
		// the tls_skip_verify TOML flag so it is never silently on. The
		// MinVersion floor keeps the rest of the connection hardened even
		// when the operator opts out of cert/hostname validation.
		opts = append(opts, gomail.WithTLSConfig(&tls.Config{ //nolint:gosec // NOSONAR S4830,S5527: operator-opted-in via tls_skip_verify
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: tlsSkipVerify,
		}))
	}
	if username != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthLogin),
			gomail.WithUsername(username),
			gomail.WithPassword(password),
		)
	} else {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthNoAuth))
	}
	return gomail.NewClient(host, opts...)
}

// classify maps a go-mail SendError or net/smtp textproto.Error into the
// backoff library's transient / permanent distinction. SendError carries an
// explicit IsTemp() flag. textproto.Error is what bubbles up from go-mail's
// dial sequence (HELO/STARTTLS/AUTH/MAIL/RCPT/DATA) when the server returns a
// 4xx/5xx reply — go-mail wraps it as "dial failed: SMTP AUTH failed: ..."
// without going through SendError, so we check both shapes. Anything else is
// treated as transient (network blip, dial timeout) so the retry loop gets a
// chance.
func (c *Channel) classify(err error) error {
	var sendErr *gomail.SendError
	if errors.As(err, &sendErr) {
		if sendErr.IsTemp() {
			return err
		}
		return backoff.Permanent(err)
	}
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		if tpErr.Code >= 500 && tpErr.Code < 600 {
			return backoff.Permanent(err)
		}
		return err
	}
	return err
}

// rejectCRLF returns a permanent error if value contains a CR or LF, which
// would allow injecting additional SMTP headers (BCC redirection, content
// spoofing). Empty input is allowed — callers that require a non-empty value
// validate that separately.
func rejectCRLF(field, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return backoff.Permanent(fmt.Errorf("smtp %s contains CR or LF, which is not allowed in a header", field))
	}
	return nil
}

// normalizeTLSMode resolves the operator-supplied tls knob, falling back to a
// port-derived default when empty. 465 → implicit; everything else →
// starttls (including 25 and 587).
func normalizeTLSMode(mode string, port int) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case tlsModeSTARTTLS, tlsModeImplicit, tlsModeNone:
		return mode
	}
	if port == 465 {
		return tlsModeImplicit
	}
	return tlsModeSTARTTLS
}

// tagRE strips HTML element boundaries; the auto-derived plain-text
// alternative does not need to preserve markup.
var tagRE = regexp.MustCompile(`<[^>]+>`)

// wsRE collapses runs of horizontal whitespace introduced by tag removal.
var wsRE = regexp.MustCompile(`[ \t]+`)

// blankLineRE collapses runs of blank lines down to one, so a stripped layout
// reads as paragraphs rather than a half-broken wall.
var blankLineRE = regexp.MustCompile(`\n{3,}`)

// htmlToText derives a usable plain-text alternative from the HTML body.
// Strategy:
//   - convert <br>, <p>, </p>, </div>, </h?>, </li> to line breaks first;
//   - drop every other tag;
//   - decode HTML entities;
//   - collapse runs of whitespace and blank lines.
//
// The result is not a faithful rendering — it is the safety net for terminal
// MUAs that have no HTML pipeline. Operators with stricter requirements
// supply a template_path.
func htmlToText(s string) string {
	r := strings.NewReplacer(
		"<br>", "\n",
		"<br/>", "\n",
		"<br />", "\n",
		"<BR>", "\n",
		"</p>", "\n\n",
		"</P>", "\n\n",
		"</div>", "\n",
		"</DIV>", "\n",
		"</h1>", "\n\n",
		"</h2>", "\n\n",
		"</h3>", "\n\n",
		"</h4>", "\n\n",
		"</h5>", "\n\n",
		"</h6>", "\n\n",
		"</li>", "\n",
		"</LI>", "\n",
		"</pre>", "\n",
		"</PRE>", "\n",
	)
	s = r.Replace(s)
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = wsRE.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	s = strings.Join(lines, "\n")
	s = blankLineRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
