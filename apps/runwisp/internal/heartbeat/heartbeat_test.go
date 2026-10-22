// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package heartbeat

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRT records each request's URL on reqs and answers with the next result
// from results (a nil error with the status), repeating the last one.
type stubRT struct {
	mu      sync.Mutex
	reqs    chan *http.Request
	results []error
}

func (s *stubRT) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	var err error
	if len(s.results) > 0 {
		err = s.results[0]
		if len(s.results) > 1 {
			s.results = s.results[1:]
		}
	}
	s.mu.Unlock()
	s.reqs <- req
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("OK")), Header: make(http.Header)}, nil
}

// startPinger runs a pinger for rawURL whose interval timer is the returned
// tick channel, and stops it at test end.
func startPinger(t *testing.T, rawURL string, rt *stubRT, logger *slog.Logger) (*Pinger, chan time.Time) {
	t.Helper()
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	p := New(rawURL, "1.8.0", logger)
	p.client = &http.Client{Transport: rt}
	tick := make(chan time.Time)
	p.after = func(d time.Duration) <-chan time.Time {
		assert.Equal(t, Interval, d)
		return tick
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return p, tick
}

func nextRequest(t *testing.T, rt *stubRT) *http.Request {
	t.Helper()
	select {
	case req := <-rt.reqs:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("no heartbeat request")
		return nil
	}
}

func TestPinger_PingsEveryInterval(t *testing.T) {
	rt := &stubRT{reqs: make(chan *http.Request, 4)}
	_, tick := startPinger(t, "https://hc-ping.com/abc", rt, nil)

	req := nextRequest(t, rt)
	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "https://hc-ping.com/abc", req.URL.String())
	assert.Equal(t, "runwisp/1.8.0", req.Header.Get("User-Agent"))

	tick <- time.Now()
	nextRequest(t, rt)
	tick <- time.Now()
	nextRequest(t, rt)
}

func TestPinger_EmptyURLNeverRequests(t *testing.T) {
	rt := &stubRT{reqs: make(chan *http.Request, 4)}
	startPinger(t, "", rt, nil)

	assert.Never(t, func() bool { return len(rt.reqs) > 0 }, 100*time.Millisecond, 5*time.Millisecond)
}

func TestPinger_SetURLWakesAndSwitches(t *testing.T) {
	rt := &stubRT{reqs: make(chan *http.Request, 4)}
	p, _ := startPinger(t, "", rt, nil)

	p.SetURL("https://a.example/one")
	assert.Equal(t, "a.example", nextRequest(t, rt).URL.Host, "setting a URL pings at once")

	p.SetURL("https://b.example/two")
	host := nextRequest(t, rt).URL.Host
	if host == "a.example" { // the first SetURL landed before Run read the URL: a pinged twice
		host = nextRequest(t, rt).URL.Host
	}
	assert.Equal(t, "b.example", host, "a new URL pings at once, without waiting a minute")

	p.SetURL("")
	assert.Never(t, func() bool { return len(rt.reqs) > 0 }, 100*time.Millisecond, 5*time.Millisecond)
}

func TestPinger_FailureLoggedOnceNoURLInLog(t *testing.T) {
	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	down := errors.New("connection refused")
	rt := &stubRT{reqs: make(chan *http.Request, 4), results: []error{down, down, down, nil}}
	_, tick := startPinger(t, "https://hc-ping.com/secret-token", rt, logger)

	nextRequest(t, rt)
	for range 3 {
		tick <- time.Now()
		nextRequest(t, rt)
	}
	tick <- time.Now() // the loop has logged the fourth result once it waits again

	out := buf.String()
	assert.Equal(t, 1, strings.Count(out, "Heartbeat ping failed"), out)
	assert.Equal(t, 1, strings.Count(out, "Heartbeat ping succeeded again"), out)
	assert.Contains(t, out, "host=hc-ping.com")
	assert.NotContains(t, out, "secret-token", "the URL is a secret")
}

func TestPing_ErrorOmitsURL(t *testing.T) {
	p := New("", "1.8.0", slog.New(slog.DiscardHandler))
	p.client = &http.Client{Transport: &stubRT{reqs: make(chan *http.Request, 1), results: []error{errors.New("boom")}}}
	err := p.ping(context.Background(), "https://hc-ping.com/secret-token")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-token")
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
