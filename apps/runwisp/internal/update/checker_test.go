// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package update

import (
	"context"
	"io"
	"net/http"
	"strings"
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
