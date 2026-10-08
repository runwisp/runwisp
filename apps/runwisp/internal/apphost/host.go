// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package apphost serves the apps connected to the daemon over its local
// socket. An app runs `sdk` units in its own process: the daemon sends it
// a run, the app streams the run's output back and reports an exit code, and
// the executor records it like any other run.
//
// Several copies of one app may connect. The first is active and serves every
// sdk unit; the others stand by and the next one takes over when it leaves.
package apphost

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime"
)

// Protocol is the Upgrade token an app sends to open its connection.
const Protocol = "runwisp-app"

// defaultHandoff is how long a handler run waits for an app to connect: the
// app connects right after the daemon it spawned comes up, and a standby copy
// takes over within about a second of the active one leaving.
const defaultHandoff = 10 * time.Second

// maxLine bounds one NDJSON message. Output lines travel inside messages, so
// this is also the longest output line an app can send in one piece.
const maxLine = 16 << 20

// message is one NDJSON line on an app connection, in either direction.
//
// Daemon to app: role {active}, run {run, task, env, params}, stop {run},
// config_ok {result}, config_error {error, restart}.
// App to daemon: output {run, stream, line}, exit {run, code}, config {doc},
// close (the app is shutting down for good, not restarting; see leave).
type message struct {
	Type    string              `json:"type"`
	Active  *bool               `json:"active,omitempty"`
	Run     string              `json:"run,omitempty"`
	Task    string              `json:"task,omitempty"`
	Env     map[string]string   `json:"env,omitempty"`
	Params  map[string]string   `json:"params,omitempty"`
	Stream  string              `json:"stream,omitempty"`
	Line    string              `json:"line,omitempty"`
	Code    *int                `json:"code,omitempty"`
	Doc     json.RawMessage     `json:"doc,omitempty"`
	Result  *model.ReloadResult `json:"result,omitempty"`
	Error   string              `json:"error,omitempty"`
	Restart bool                `json:"restart,omitempty"`
}

// Host tracks the connected apps and implements executor.Backend for sdk
// units.
type Host struct {
	after func(time.Duration) <-chan time.Time

	// config and reload are set by AcceptConfig on a daemon whose config an
	// app supplies; nil otherwise.
	config *config.DocumentSource
	reload func() (model.ReloadResult, error)

	mu       sync.Mutex
	sessions []*session // in connection order; sessions[0] is active
	changed  chan struct{}
	closed   bool
	// quit is set when the last app said it is shutting down, so an idle
	// daemon need not wait for it to come back.
	quit bool
}

// AcceptConfig lets the active app replace the daemon's config. Each document
// it sends is set on src and applied by reload, exactly like an edited
// runwisp.toml followed by `runwisp reload`: validated whole first, rejected
// whole on any error. Call before the first app connects.
func (h *Host) AcceptConfig(src *config.DocumentSource, reload func() (model.ReloadResult, error)) {
	h.config = src
	h.reload = reload
}

// ExitWhenIdle calls exit once no app has been connected for idle, counting
// from now, for a daemon that exists only to serve its app. It exits right
// away when the last app said it is shutting down.
func (h *Host) ExitWhenIdle(idle time.Duration, exit func()) {
	go func() {
		for {
			h.mu.Lock()
			connected, changed, closed, quit := len(h.sessions), h.changed, h.closed, h.quit
			h.mu.Unlock()
			if closed {
				return
			}
			if connected == 0 && quit {
				exit()
				return
			}
			var idleC <-chan time.Time // nil, never fires, while an app is connected
			if connected == 0 {
				idleC = h.after(idle)
			}
			select {
			case <-changed:
			case <-idleC:
				exit()
				return
			}
		}
	}()
}

// New returns a Host with no apps connected.
func New() *Host {
	return &Host{after: time.After, changed: make(chan struct{})}
}

// ServeHTTP upgrades a request to an app connection and serves it until either
// side closes it. The caller decides who may connect: the daemon mounts this on
// its local socket only.
func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), Protocol) {
		w.Header().Set("Upgrade", Protocol)
		http.Error(w, "expected Upgrade: "+Protocol, http.StatusUpgradeRequired)
		return
	}
	conn, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "connection can't be upgraded", http.StatusInternalServerError)
		return
	}
	// The server's read/write timeouts are for requests; this connection lives
	// as long as the app does.
	_ = conn.SetDeadline(time.Time{})
	if _, err := buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + Protocol + "\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		_ = conn.Close()
		return
	}
	if err := buf.Flush(); err != nil {
		_ = conn.Close()
		return
	}
	h.ServeConn(struct {
		io.Reader
		io.Writer
		io.Closer
	}{buf.Reader, conn, conn})
}

// ServeConn serves one app connection until it closes. Its runs that are
// still going end as failed when it does.
func (h *Host) ServeConn(conn io.ReadWriteCloser) {
	s := newSession(conn)
	if !h.add(s) {
		_ = conn.Close()
		return
	}
	defer h.remove(s)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	for scanner.Scan() {
		var m message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			slog.Warn("App sent a malformed message; closing its connection", "err", err)
			return
		}
		switch m.Type {
		case "config":
			// Off the read loop: a reload may stop a handler run and wait for
			// the exit this loop has to read.
			go h.applyConfig(s, m.Doc)
		case "close":
			h.leave(s)
		default:
			s.handle(m)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		slog.Debug("App connection ended", "err", err)
	}
}

// Close disconnects every app and refuses new ones. Runs still in flight end
// as failed.
func (h *Host) Close() {
	h.mu.Lock()
	h.closed = true
	sessions := slices.Clone(h.sessions)
	h.broadcastLocked()
	h.mu.Unlock()
	for _, s := range sessions {
		_ = s.conn.Close()
	}
}

func (h *Host) add(s *session) bool {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return false
	}
	h.sessions = append(h.sessions, s)
	h.quit = false
	connected := len(h.sessions)
	h.broadcastLocked()
	h.mu.Unlock()

	s.sendRole(connected == 1)
	slog.Info("App connected", "active", connected == 1, "connected", connected)
	return true
}

// detachLocked takes s out of the connected apps and returns the copy that
// takes over from it, if s was active and another is connected.
func (h *Host) detachLocked(s *session) (next *session) {
	wasActive := len(h.sessions) > 0 && h.sessions[0] == s
	h.sessions = slices.DeleteFunc(h.sessions, func(other *session) bool { return other == s })
	if wasActive && len(h.sessions) > 0 {
		next = h.sessions[0]
	}
	h.broadcastLocked()
	return next
}

func (h *Host) remove(s *session) {
	h.mu.Lock()
	next := h.detachLocked(s)
	connected := len(h.sessions)
	h.mu.Unlock()

	_ = s.conn.Close()
	s.disconnect()
	if next != nil {
		next.sendRole(true)
	}
	slog.Info("App disconnected", "connected", connected)
}

// leave takes an app that is shutting down out of service. Its runs keep
// going until they end. When other copies are connected, the next one takes
// over and the app is told it is on standby, so it stops what only the active
// copy runs. When it was the last, the daemon may exit right away
// (ExitWhenIdle) and stops its runs the way it stops any run at shutdown.
func (h *Host) leave(s *session) {
	h.mu.Lock()
	next := h.detachLocked(s)
	h.quit = len(h.sessions) == 0
	quit := h.quit
	h.mu.Unlock()

	if !quit {
		s.sendRole(false)
	}
	if next != nil {
		next.sendRole(true)
	}
}

// broadcastLocked wakes everything waiting for the set of apps to change.
func (h *Host) broadcastLocked() {
	close(h.changed)
	h.changed = make(chan struct{})
}

// active returns the app serving sdk units, waiting up to the handoff window
// for one to connect. nil when none did.
func (h *Host) active(stop <-chan struct{}) *session {
	deadline := h.after(defaultHandoff)
	for {
		h.mu.Lock()
		if len(h.sessions) > 0 {
			s := h.sessions[0]
			h.mu.Unlock()
			return s
		}
		changed := h.changed
		h.mu.Unlock()
		select {
		case <-changed:
		case <-deadline:
			return nil
		case <-stop:
			return nil
		}
	}
}

// applyConfig applies a config document from an app and tells it the outcome.
//
// ponytail: pushes from one app are applied as they arrive, so two in flight
// may answer out of order; the SDK keeps one in flight at a time.
func (h *Host) applyConfig(s *session, doc json.RawMessage) {
	reply := message{Type: "config_error"}
	switch {
	case h.reload == nil:
		reply.Error = "this daemon reads its config from runwisp.toml, not from an app"
	case !h.isActive(s):
		reply.Error = "this copy of the app is on standby; the active copy's config applies"
	default:
		h.config.Set(doc)
		result, err := h.reload()
		if err == nil {
			reply = message{Type: "config_ok", Result: &result}
		} else {
			reply.Error = err.Error()
			reply.Restart = errors.Is(err, runtime.ErrRestartRequired)
		}
	}
	if err := s.send(reply); err != nil {
		slog.Debug("Failed to answer an app's config", "err", err)
	}
}

func (h *Host) isActive(s *session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions) > 0 && h.sessions[0] == s
}

// session is one connected app.
type session struct {
	conn io.ReadWriteCloser

	wmu sync.Mutex
	enc *json.Encoder

	mu   sync.Mutex
	runs map[string]*handlerRun
	gone bool
}

func newSession(conn io.ReadWriteCloser) *session {
	return &session{conn: conn, enc: json.NewEncoder(conn), runs: map[string]*handlerRun{}}
}

func (s *session) send(m message) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.enc.Encode(m)
}

func (s *session) sendRole(active bool) {
	if err := s.send(message{Type: "role", Active: &active}); err != nil {
		slog.Debug("Failed to tell an app its role", "err", err)
	}
}

func (s *session) handle(m message) {
	switch m.Type {
	case "output":
		r := s.run(m.Run)
		if r == nil {
			return // a run that already ended, e.g. stopped past graceful_stop
		}
		w := r.stdout
		if m.Stream == "stderr" {
			w = r.stderr
		}
		_, _ = io.WriteString(w, m.Line+"\n")
	case "exit":
		if r := s.run(m.Run); r != nil && m.Code != nil {
			r.finish(*m.Code)
		}
	default:
		// Unknown types are ignored, so an app can be newer than the daemon.
	}
}

func (s *session) run(id string) *handlerRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[id]
}

// begin registers a run and sends it to the app.
func (s *session) begin(r *handlerRun, m message) error {
	s.mu.Lock()
	if s.gone {
		s.mu.Unlock()
		return errors.New("the app disconnected")
	}
	s.runs[m.Run] = r
	s.mu.Unlock()
	if err := s.send(m); err != nil {
		s.forget(m.Run)
		return fmt.Errorf("send the run to the app: %w", err)
	}
	return nil
}

func (s *session) forget(id string) {
	s.mu.Lock()
	delete(s.runs, id)
	s.mu.Unlock()
}

// disconnect ends every run the app was serving.
func (s *session) disconnect() {
	s.mu.Lock()
	s.gone = true
	runs := s.runs
	s.runs = map[string]*handlerRun{}
	s.mu.Unlock()
	for _, r := range runs {
		_, _ = io.WriteString(r.stderr, "runwisp: the app running this task disconnected\n")
		r.finish(-1)
	}
}
