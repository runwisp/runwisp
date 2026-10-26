// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package heartbeat pings [daemon] heartbeat_url, a dead man's switch such as
// healthchecks.io, while the daemon runs. A monitor watching that URL alerts
// when the pings stop, which covers the one failure the daemon can't report
// itself: being down. The ping is strictly optional and best-effort: it runs
// on its own goroutine, never blocks the daemon, and a failure is only logged.
package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	// Interval is the time between pings. Monitors are set up with a period
	// and a grace time around it; a minute suits the usual defaults.
	Interval       = time.Minute
	requestTimeout = 10 * time.Second
)

// Pinger owns the heartbeat goroutine. The URL usually embeds a secret check
// token, so it is never logged: log lines name the host only.
type Pinger struct {
	userAgent string
	client    *http.Client
	logger    *slog.Logger
	after     func(time.Duration) <-chan time.Time // time.After; tests drive it
	wake      chan struct{}                        // nudges Run when SetURL changes the URL

	mu      sync.Mutex
	url     string
	failing bool // the last ping failed; failures are logged once per outage
}

// New builds a pinger for rawURL (empty means off). version is the running
// daemon's, sent in the User-Agent.
func New(rawURL, version string, logger *slog.Logger) *Pinger {
	return &Pinger{
		userAgent: "runwisp/" + version,
		client:    &http.Client{Timeout: requestTimeout},
		logger:    logger,
		after:     time.After,
		wake:      make(chan struct{}, 1),
		url:       rawURL,
	}
}

// SetURL applies a reloaded heartbeat_url. A new URL is pinged right away; an
// empty one stops the pings.
func (p *Pinger) SetURL(rawURL string) {
	p.mu.Lock()
	changed := p.url != rawURL
	p.url = rawURL
	if changed {
		p.failing = false
	}
	p.mu.Unlock()
	if changed {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

// Run pings at once and then every Interval until ctx is cancelled. With no
// URL it idles until SetURL sets one.
func (p *Pinger) Run(ctx context.Context) {
	var pinged string         // the URL the last ping went to; a wake for it is stale
	var next <-chan time.Time // nil while off: wait for a wake only
	for {
		u := p.currentURL()
		switch {
		case u == "":
			pinged, next = "", nil
		case next == nil || u != pinged: // first ping, a new URL, or the timer fired
			p.report(u, p.ping(ctx, u))
			pinged, next = u, p.after(Interval)
		}
		select {
		case <-ctx.Done():
			return
		case <-next:
			next = nil
		case <-p.wake:
		}
	}
}

func (p *Pinger) currentURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url
}

// ping sends one GET. The returned error never contains the URL.
func (p *Pinger) ping(ctx context.Context, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return stripURL(err)
	}
	req.Header.Set("User-Agent", p.userAgent)
	res, err := p.client.Do(req)
	if err != nil {
		return stripURL(err)
	}
	_ = res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("unexpected status %d", res.StatusCode)
	}
	return nil
}

// report logs a ping result when it differs from the last one for the same
// URL: the first failure of an outage, and the first success after it.
func (p *Pinger) report(rawURL string, err error) {
	if err != nil && errors.Is(err, context.Canceled) {
		return // daemon shutdown, not an outage
	}
	p.mu.Lock()
	if p.url != rawURL { // replaced mid-ping; SetURL reset the state
		p.mu.Unlock()
		return
	}
	wasFailing := p.failing
	p.failing = err != nil
	p.mu.Unlock()

	host := hostOf(rawURL)
	switch {
	case err != nil && !wasFailing:
		p.logger.Warn("Heartbeat ping failed; retrying every minute", "host", host, "err", err)
	case err == nil && wasFailing:
		p.logger.Info("Heartbeat ping succeeded again", "host", host)
	}
}

// stripURL drops the *url.Error wrapper, whose message repeats the URL.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}
