// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package sendmail implements the local-MTA notify channel: it composes a
// plain-text RFC 5322 message and pipes it to the host's sendmail binary,
// which is how crond has always delivered MAILTO output.
//
// It exists alongside the smtp channel because the two answer different
// questions. smtp needs a relay host, a port, a TLS mode and usually
// credentials — real configuration an operator has to go and find. A box
// running cron jobs that mailed their output already has a working MTA
// (Postfix, exim, msmtp, ssmtp), already knows where to relay, and already
// holds the credentials. Reproducing MAILTO through it is one line of TOML
// instead of six, and the queueing, retrying and rewriting stay the MTA's job
// — which is the boundary the project draws around mail.
//
// This does not make RunWisp a mailer. It execs a binary the operator already
// installed, with the message on stdin and no recipient on the command line.
package sendmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"os/exec"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// exTempFail is sysexits.h EX_TEMPFAIL — "the operation failed, but may
// succeed later". Every sendmail-compatible binary uses it for a queue it
// couldn't write or a relay it couldn't reach, and it is the only exit status
// that means "ask again". Anything else (bad address, no permission, broken
// config) will fail identically on the next attempt.
const exTempFail = 75

// defaultPaths are where a sendmail-compatible binary lives on the platforms
// RunWisp supports, in the order the MTAs themselves install it. Postfix,
// exim, msmtp and ssmtp all provide /usr/sbin/sendmail; the others are the
// historical locations still used by some distributions and by macOS.
var defaultPaths = []string{
	"/usr/sbin/sendmail",
	"/usr/lib/sendmail",
	"/usr/bin/sendmail",
}

// maxStderr caps how much of the MTA's complaint is carried into the error.
// A misconfigured relay can be extremely talkative, and this string ends up in
// a log line and in a notify_delivery_failed event.
const maxStderr = 4 << 10

// Channel pipes rendered notifications into the host MTA.
type Channel struct {
	id        string
	path      string
	from      string
	replyTo   string
	to        []string
	cc        []string
	bcc       []string
	backoff   notify.BackoffConfig
	renderer  render.Renderer
	now       func() time.Time
	lookPath  func(string) (string, error)
	runBinary runner
}

// runner is the exec seam. It receives the resolved binary, its arguments and
// the message, and reports how the binary exited. Tests substitute one instead
// of installing an MTA; production uses runCommand.
type runner func(ctx context.Context, path string, args []string, stdin []byte) error

// Config is the input the factory needs to build a Channel.
type Config struct {
	ID string
	// Path is an explicit sendmail binary. Empty means search defaultPaths and
	// then $PATH, which is what an operator who just wants "the system mailer"
	// should get.
	Path       string
	From       string
	ReplyTo    string
	Recipients []string
	CC         []string
	BCC        []string
	Backoff    notify.BackoffConfig
	Renderer   render.Renderer

	// Now, LookPath and Run are test seams. Nil means the real thing.
	Now      func() time.Time
	LookPath func(string) (string, error)
	Run      runner
}

// New constructs the channel and validates the static configuration.
//
// It deliberately does not check that the binary exists. Resolution happens on
// the first send, for the same reason run-as users are resolved at run time
// rather than config load: the daemon may legitimately boot before the MTA is
// installed or before its package finishes configuring, and a config that
// validates on one machine and refuses on another is worse than a delivery
// failure that says exactly what is missing.
func New(cfg Config) (*Channel, error) {
	if strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("sendmail channel %q: from is required", cfg.ID)
	}
	if len(cfg.Recipients) == 0 {
		return nil, fmt.Errorf("sendmail channel %q: at least one recipient is required", cfg.ID)
	}
	if cfg.Renderer == nil {
		return nil, fmt.Errorf("sendmail channel %q: renderer is required", cfg.ID)
	}
	bo := cfg.Backoff
	if bo.IsZero() {
		bo = notify.DefaultBackoff()
	}

	c := &Channel{
		id:        cfg.ID,
		path:      strings.TrimSpace(cfg.Path),
		from:      strings.TrimSpace(cfg.From),
		replyTo:   strings.TrimSpace(cfg.ReplyTo),
		to:        append([]string(nil), cfg.Recipients...),
		cc:        append([]string(nil), cfg.CC...),
		bcc:       append([]string(nil), cfg.BCC...),
		backoff:   bo,
		renderer:  cfg.Renderer,
		now:       cfg.Now,
		lookPath:  cfg.LookPath,
		runBinary: cfg.Run,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.lookPath == nil {
		c.lookPath = exec.LookPath
	}
	if c.runBinary == nil {
		c.runBinary = runCommand
	}
	return c, nil
}

func (c *Channel) ID() string                  { return c.id }
func (c *Channel) Close(context.Context) error { return nil }
func (c *Channel) String() string              { return "sendmail:" + c.id }

// Execute renders the event, composes the message, and pipes it to the MTA.
func (c *Channel) Execute(ctx context.Context, ev *notify.Event) error {
	rendered, err := c.renderer.Render(ev)
	if err != nil {
		return fmt.Errorf("%s: render: %w", c, err)
	}
	subject := strings.TrimSpace(rendered.Title)
	if subject == "" {
		subject = "RunWisp notification"
	}

	msg, err := c.compose(subject, string(rendered.Body))
	if err != nil {
		return fmt.Errorf("%s: %w", c, err)
	}

	path, err := c.resolve()
	if err != nil {
		return fmt.Errorf("%s: %w", c, err)
	}

	op := func() error {
		// -t takes the recipients from the headers, so no address ever reaches
		// the command line — an address cannot become an argument, let alone a
		// flag. -i stops a body line consisting of a single "." from being read
		// as end-of-input, which would silently truncate a job's output.
		return classifyExit(c.runBinary(ctx, path, []string{"-t", "-i"}, msg))
	}
	if err := notify.RetryWithBackoff(ctx, c.backoff, op); err != nil {
		return fmt.Errorf("%s: %w", c, err)
	}
	return nil
}

// resolve finds the sendmail binary. A configured path is used verbatim and
// its absence is a permanent error naming it; otherwise the well-known
// locations are tried in order and $PATH last.
func (c *Channel) resolve() (string, error) {
	if c.path != "" {
		return c.path, nil
	}
	for _, candidate := range defaultPaths {
		if _, err := c.lookPath(candidate); err == nil {
			return candidate, nil
		}
	}
	if found, err := c.lookPath("sendmail"); err == nil {
		return found, nil
	}
	return "", backoff.Permanent(fmt.Errorf(
		"no sendmail binary found in %s or $PATH; install an MTA (Postfix, exim, msmtp) "+
			"or set sendmail_path", strings.Join(defaultPaths, ", ")))
}

// compose builds the RFC 5322 message handed to the MTA on stdin.
func (c *Channel) compose(subject, body string) ([]byte, error) {
	if err := c.rejectHeaderCRLF(subject); err != nil {
		return nil, err
	}

	var b strings.Builder
	writeHeader(&b, "From", c.from)
	if c.replyTo != "" {
		writeHeader(&b, "Reply-To", c.replyTo)
	}
	writeHeader(&b, "To", strings.Join(c.to, ", "))
	if len(c.cc) > 0 {
		writeHeader(&b, "Cc", strings.Join(c.cc, ", "))
	}
	if len(c.bcc) > 0 {
		// -t reads Bcc from here and strips the header before delivery, which
		// is the whole reason the addresses are not passed as arguments.
		writeHeader(&b, "Bcc", strings.Join(c.bcc, ", "))
	}
	// RFC 2047 encoding is a no-op for an all-ASCII subject and the difference
	// between a readable and a mojibake one otherwise.
	writeHeader(&b, "Subject", mime.QEncoding.Encode("utf-8", subject))
	writeHeader(&b, "Date", c.now().Format(time.RFC1123Z))
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", "text/plain; charset=utf-8")
	// Tell autoresponders and mailing lists this is machine-generated, so a
	// failing task can't strike up a correspondence with an out-of-office bot.
	writeHeader(&b, "Auto-Submitted", "auto-generated")
	writeHeader(&b, "X-Auto-Response-Suppress", "All")
	writeHeader(&b, "User-Agent", "runwisp-notify/1")
	b.WriteString("\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

func writeHeader(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\n")
}

// rejectHeaderCRLF guards every value that lands in a header. The body is
// exempt on purpose: it is separated from the headers by the blank line, so a
// newline in it is just a newline.
func (c *Channel) rejectHeaderCRLF(subject string) error {
	if err := notify.RejectHeaderCRLF("sendmail subject", subject); err != nil {
		return err
	}
	if err := notify.RejectHeaderCRLF("sendmail from", c.from); err != nil {
		return err
	}
	if err := notify.RejectHeaderCRLF("sendmail reply-to", c.replyTo); err != nil {
		return err
	}
	for _, group := range [][]string{c.to, c.cc, c.bcc} {
		for _, addr := range group {
			if err := notify.RejectHeaderCRLF("sendmail recipient", addr); err != nil {
				return err
			}
		}
	}
	return nil
}

// runCommand is the production runner: pipe the message in, capture the
// diagnosis, and translate the exit status into transient-or-permanent.
func runCommand(ctx context.Context, path string, args []string, stdin []byte) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &ExitError{Code: exitErr.ExitCode(), Stderr: trimStderr(stderr.String())}
	}
	// The binary could not be started at all: it is missing, not executable, or
	// not a binary. Retrying will not change any of those.
	return backoff.Permanent(fmt.Errorf("run %s: %w", path, err))
}

// ExitError reports a sendmail binary that ran and exited non-zero. It is
// exported so a test runner can return one without fabricating an
// *exec.ExitError, which has no usable constructor.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("sendmail exited %d", e.Code)
	if e.Code == exTempFail {
		msg += " (EX_TEMPFAIL)"
	}
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// Permanent reports whether retrying this exit status is pointless.
func (e *ExitError) Permanent() bool { return e.Code != exTempFail }

// classifyExit decides whether the retry loop should try again. It sits in the
// channel rather than in the runner so a test fake returning a bare ExitError
// gets exactly the production decision — otherwise "does EX_TEMPFAIL retry?"
// would only ever be exercised by the code path tests don't run.
func classifyExit(err error) error {
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Permanent() {
		return backoff.Permanent(exitErr)
	}
	return err
}

func trimStderr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxStderr {
		s = s[:maxStderr] + "…"
	}
	return s
}
