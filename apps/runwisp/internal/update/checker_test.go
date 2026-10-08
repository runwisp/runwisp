// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package update

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stubRT struct {
	status int
	body   string
	err    error
}

func (s stubRT) RoundTrip(*http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Header:     make(http.Header),
	}, nil
}

func newTestChecker(current string, rt http.RoundTripper) *Checker {
	c := NewChecker(current, "linux", "amd64", true)
	c.baseURL = "http://concierge.test"
	c.client = &http.Client{Transport: rt}
	return c
}

func TestCheckOnceFlipsAvailable(t *testing.T) {
	c := newTestChecker("0.2.0", stubRT{status: 200, body: `{"version":"v0.3.0","prerelease":false,"ttl":21600}`})
	next := c.checkOnce(context.Background())
	if want := 21600 * time.Second; next != want {
		t.Fatalf("next = %v, want %v", next, want)
	}
	available, latest := c.Status()
	if !available || latest != "v0.3.0" {
		t.Fatalf("Status() = (%v, %q), want (true, \"v0.3.0\")", available, latest)
	}
}

func TestCheckOnceUpToDate(t *testing.T) {
	c := newTestChecker("0.3.0", stubRT{status: 200, body: `{"version":"v0.3.0","prerelease":false,"ttl":0}`})
	if next := c.checkOnce(context.Background()); next != fallbackInterval {
		t.Fatalf("next = %v, want fallback %v (ttl<=0)", next, fallbackInterval)
	}
	if available, _ := c.Status(); available {
		t.Fatal("Status() available = true, want false for equal versions")
	}
}

func TestCheckOnceIgnoresPrerelease(t *testing.T) {
	c := newTestChecker("0.2.0", stubRT{status: 200, body: `{"version":"v0.3.0-rc.1","prerelease":true,"ttl":100}`})
	c.checkOnce(context.Background())
	if available, _ := c.Status(); available {
		t.Fatal("Status() available = true, want false for a prerelease latest")
	}
}

func TestCheckOnceClampsShortTTL(t *testing.T) {
	c := newTestChecker("0.2.0", stubRT{status: 200, body: `{"version":"v0.3.0","prerelease":false,"ttl":5}`})
	if next := c.checkOnce(context.Background()); next != minInterval {
		t.Fatalf("next = %v, want clamp %v", next, minInterval)
	}
}

func TestCheckOnceKeepsStateOnError(t *testing.T) {
	c := newTestChecker("0.2.0", stubRT{status: 200, body: `{"version":"v0.3.0","prerelease":false,"ttl":21600}`})
	c.checkOnce(context.Background())

	c.client = &http.Client{Transport: stubRT{status: 502, body: `{"error":"upstream"}`}}
	if next := c.checkOnce(context.Background()); next != fallbackInterval {
		t.Fatalf("next = %v, want fallback %v on error", next, fallbackInterval)
	}
	if available, latest := c.Status(); !available || latest != "v0.3.0" {
		t.Fatalf("Status() = (%v, %q), want prior state kept", available, latest)
	}
}

// A regression guard for the base URL itself: NewChecker (as used by the real
// daemon, with no test override) must point at the real concierge service,
// not a placeholder — a local address here means the shipped feature never
// reaches concierge.runwisp.com in production.
func TestNewChecker_DefaultsToRealConciergeURL(t *testing.T) {
	c := NewChecker("0.3.0", "linux", "amd64", true)
	if c.baseURL != "https://concierge.runwisp.com" {
		t.Fatalf("baseURL = %q, want https://concierge.runwisp.com", c.baseURL)
	}
}

func TestIsRelease(t *testing.T) {
	cases := map[string]bool{
		"0.0.0-dev": false,
		"0.0.0":     false,
		"garbage":   false,
		"0.3.1":     true,
		"v0.3.1":    true,
		"1.0.0-rc1": true,
	}
	for in, want := range cases {
		if got := IsRelease(in); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRunSkipsDevBuild(t *testing.T) {
	// A dev build must never reach the network: a RoundTripper that fails the
	// test if called proves Run() returns before any request.
	c := newTestChecker("0.0.0-dev", stubRT{err: context.Canceled})
	c.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("dev build must not perform an update check")
		return nil, nil
	})}
	c.Run(context.Background()) // returns immediately; no hang, no request
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckOnceSendsInstallSource(t *testing.T) {
	t.Setenv("RUNWISP_INSTALL_SOURCE", "npx")
	var got string
	c := newTestChecker("0.2.0", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL.Query().Get("source")
		return stubRT{status: 200, body: `{"version":"v0.3.0"}`}.RoundTrip(r)
	}))
	c.checkOnce(context.Background())
	if got != "npx" {
		t.Fatalf("source = %q, want \"npx\"", got)
	}
}

func TestInstallSourceMarkerAndFallback(t *testing.T) {
	t.Setenv("RUNWISP_INSTALL_SOURCE", "")
	if got := installSource(); got != "other" {
		t.Fatalf("no env, no marker: installSource() = %q, want \"other\"", got)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(exe), ".runwisp-source")
	if err := os.WriteFile(marker, []byte("script\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(marker) })
	if got := installSource(); got != "script" {
		t.Fatalf("marker: installSource() = %q, want \"script\"", got)
	}
}

func TestSourceOfBinaryHomebrew(t *testing.T) {
	for exe, want := range map[string]string{
		"/opt/homebrew/Cellar/runwisp/1.5.1/bin/runwisp":              "homebrew",
		"/home/linuxbrew/.linuxbrew/Cellar/runwisp/1.5.1/bin/runwisp": "homebrew",
		"/usr/local/bin/runwisp":                                      "other",
		"/opt/homebrew/Cellar/runwisp-dev/1.0/bin/runwisp":            "other",
	} {
		if got := sourceOfBinary(exe); got != want {
			t.Errorf("sourceOfBinary(%q) = %q, want %q", exe, got, want)
		}
	}
}

// TestSetEnabledFalseCancelsInFlightCheck: turning check_updates off must stop
// a request already on the wire, not just discard its answer.
func TestSetEnabledFalseCancelsInFlightCheck(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	c := newTestChecker("0.2.0", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
		return nil, r.Context().Err()
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	<-started
	c.SetEnabled(false)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("disabling the checker left the in-flight request running")
	}
}

// TestSetEnabledTogglesARunningChecker covers a reload flipping check_updates:
// a disabled checker never reaches out, turning it on checks right away, and
// turning it off clears the result it had.
func TestSetEnabledTogglesARunningChecker(t *testing.T) {
	var calls atomic.Int32
	c := newTestChecker("0.2.0", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return stubRT{status: 200, body: `{"version":"v0.3.0","ttl":21600}`}.RoundTrip(r)
	}))
	c.SetEnabled(false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("disabled checker made %d requests", n)
	}

	c.SetEnabled(true)
	deadline := time.Now().Add(time.Second)
	for available, _ := c.Status(); !available; available, _ = c.Status() {
		if time.Now().After(deadline) {
			t.Fatal("enabling the checker did not trigger a check")
		}
		time.Sleep(5 * time.Millisecond)
	}

	c.SetEnabled(false)
	if available, latest := c.Status(); available || latest != "" {
		t.Fatalf("disabling kept a stale result: available=%v latest=%q", available, latest)
	}
}
