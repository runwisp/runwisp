// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package update polls concierge.runwisp.com for the latest published release
// and reports whether the running daemon is out of date. It is strictly
// best-effort and optional: every failure is swallowed, the check never blocks
// the daemon, and with the NIC unplugged the daemon runs exactly as before —
// the indicator simply never lights up.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultBaseURL is the concierge stats endpoint. Overridable in tests via the
// Checker.baseURL field (same package).
const DefaultBaseURL = "https://concierge.runwisp.com"

const (
	// fallbackInterval is used when the response carries no usable ttl or the
	// request failed — we still recheck, just on a conservative cadence.
	fallbackInterval = 6 * time.Hour
	// minInterval clamps a too-eager server ttl so a misconfigured concierge
	// can never make the daemon hammer it.
	minInterval    = 1 * time.Hour
	requestTimeout = 10 * time.Second
)

// checkResponse mirrors the /v1/check 200 body (see packages concierge spec).
type checkResponse struct {
	Version    string `json:"version"`
	Prerelease bool   `json:"prerelease"`
	TTL        int    `json:"ttl"`
}

// Checker owns the update-availability state and the goroutine that refreshes
// it. The zero value is not usable; construct with NewChecker.
type Checker struct {
	current string // running daemon version (may lack a leading "v")
	os      string
	arch    string
	source  string // install channel, see installSource

	baseURL string
	client  *http.Client
	wake    chan struct{} // nudges Run when SetEnabled flips the toggle

	mu        sync.RWMutex
	enabled   bool
	available bool
	latest    string
	// cancelCheck cancels the check in flight, if any; see startCheck.
	cancelCheck context.CancelFunc
}

// NewChecker builds a checker for the running build. current is version.Version;
// goos/goarch are runtime.GOOS/GOARCH (stats only). enabled reflects the
// [daemon] check_updates toggle.
func NewChecker(current, goos, goarch string, enabled bool) *Checker {
	return &Checker{
		current: current,
		os:      goos,
		arch:    goarch,
		source:  installSource(),
		enabled: enabled,
		baseURL: DefaultBaseURL,
		client:  &http.Client{Timeout: requestTimeout},
		wake:    make(chan struct{}, 1),
	}
}

// SetEnabled applies a reloaded [daemon] check_updates. Turning it on checks
// right away; turning it off cancels a check in flight, stops further checks,
// and clears any "newer release" result, since nothing would keep it current.
func (c *Checker) SetEnabled(enabled bool) {
	c.mu.Lock()
	changed := c.enabled != enabled
	c.enabled = enabled
	if !enabled {
		c.available, c.latest = false, ""
		if c.cancelCheck != nil {
			c.cancelCheck()
		}
	}
	c.mu.Unlock()
	if changed {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// startCheck returns the context for one check, which SetEnabled(false)
// cancels, or false while checking is off. Pair with finishCheck.
func (c *Checker) startCheck(ctx context.Context) (context.Context, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return nil, false
	}
	checkCtx, cancel := context.WithCancel(ctx)
	c.cancelCheck = cancel
	return checkCtx, true
}

func (c *Checker) finishCheck() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelCheck()
	c.cancelCheck = nil
}

// installSource reports how this binary was installed. The same
// release binary ships through every channel, so it can't be baked in at build
// time: the Docker image and npm wrapper set RUNWISP_INSTALL_SOURCE
// (docker / npm / npx), get.runwisp.com writes "script" to a .runwisp-source
// file next to the binary, and Homebrew is recognised by its Cellar path.
// Anything else is "other".
func installSource() string {
	if s := os.Getenv("RUNWISP_INSTALL_SOURCE"); s != "" {
		return s
	}
	exe, err := os.Executable()
	if err != nil {
		return "other"
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "other"
	}
	return sourceOfBinary(exe)
}

// sourceOfBinary classifies the resolved binary path when no env var says.
func sourceOfBinary(exe string) string {
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), ".runwisp-source")); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	// brew always installs into <prefix>/Cellar/<formula>/<version>/bin, on
	// macOS and Linuxbrew alike, and links it from <prefix>/bin.
	if strings.Contains(filepath.ToSlash(exe), "/Cellar/runwisp/") {
		return "homebrew"
	}
	return "other"
}

// Status reports whether a newer release exists and, if so, its version string
// (as concierge returned it, e.g. "v0.3.0"). Safe for concurrent reads; the
// server reads it per /api/daemon request.
func (c *Checker) Status() (available bool, latest string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.available, c.latest
}

// Run checks once immediately, then reschedules using the ttl each response
// carries. It returns when ctx is cancelled. A non-release (dev) build returns
// immediately without ever reaching out; a disabled checker idles until
// SetEnabled turns it on.
func (c *Checker) Run(ctx context.Context) {
	if !IsRelease(c.current) {
		return
	}
	for {
		var next <-chan time.Time // nil while disabled: wait for a wake only
		if checkCtx, ok := c.startCheck(ctx); ok {
			next = time.After(c.checkOnce(checkCtx))
			c.finishCheck()
		}
		select {
		case <-ctx.Done():
			return
		case <-next:
		case <-c.wake:
		}
	}
}

// checkOnce performs one GET and, on success, updates the cached state. It
// returns how long to wait before the next check. Every failure path keeps the
// last known state and returns the fallback interval — an outdated indicator
// that already lit up stays lit through a transient outage.
func (c *Checker) checkOnce(ctx context.Context) time.Duration {
	resp, err := c.fetch(ctx)
	if err != nil {
		slog.Debug("update check failed", "err", err)
		return fallbackInterval
	}

	available := !resp.Prerelease && isNewer(resp.Version, c.current)
	c.mu.Lock()
	if c.enabled { // turned off mid-request: keep the cleared state
		c.available = available
		c.latest = resp.Version
	}
	c.mu.Unlock()

	if resp.TTL <= 0 {
		return fallbackInterval
	}
	return max(time.Duration(resp.TTL)*time.Second, minInterval)
}

func (c *Checker) fetch(ctx context.Context) (checkResponse, error) {
	q := url.Values{}
	q.Set("current", c.current)
	q.Set("os", c.os)
	q.Set("arch", c.arch)
	q.Set("source", c.source)
	u := c.baseURL + "/v1/check?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return checkResponse{}, err
	}
	req.Header.Set("User-Agent", "runwisp/"+c.current)
	req.Header.Set("Accept", "application/json")

	res, err := c.client.Do(req)
	if err != nil {
		return checkResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return checkResponse{}, errors.New("unexpected status " + http.StatusText(res.StatusCode))
	}

	var body checkResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return checkResponse{}, err
	}
	return body, nil
}

// IsRelease reports whether current looks like a published release rather than a
// dev build. The build default is "0.0.0-dev"; any 0.0.0 base (with or without a
// prerelease suffix) is treated as dev and never triggers a network check.
func IsRelease(current string) bool {
	base := normalize(current)
	if i := strings.IndexAny(base, "-+"); i >= 0 {
		base = base[:i]
	}
	return semver.IsValid(base) && base != "v0.0.0"
}

// isNewer reports whether latest is a strictly higher semver than current.
func isNewer(latest, current string) bool {
	lv, cv := normalize(latest), normalize(current)
	if !semver.IsValid(lv) || !semver.IsValid(cv) {
		return false
	}
	return semver.Compare(lv, cv) > 0
}

// normalize adds the leading "v" the golang.org/x/mod/semver package requires.
func normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
