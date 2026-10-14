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
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime"
)

// Protocol is the Upgrade token an app sends to open its connection. The
// number is the protocol version: a daemon answers any other token with 426,
// so an SDK and a binary that don't match fail on connect, not mid-run.
const Protocol = "runwisp-app/1"

// defaultHandoff is how long a handler run waits for an app to connect: the
// app connects right after the daemon it spawned comes up, and a standby copy
// takes over within about a second of the active one leaving.
const defaultHandoff = 10 * time.Second

// defaultWriteTimeout is how long one write to an app may take. An app that
// stays connected but stops reading would otherwise block the run manager in
// the middle of a send; past this its connection is closed and its runs fail.
const defaultWriteTimeout = 10 * time.Second

// maxQueuedNotices bounds the overlap and missed messages waiting for a slow
// app; past it they are dropped.
const maxQueuedNotices = 64

// maxHeldMissed bounds the missed-firing messages held for the first app to
// connect.
const maxHeldMissed = 100

// maxLine bounds one NDJSON message. Output lines travel inside messages, so
// this is also the longest output line an app can send in one piece.
const maxLine = 16 << 20

// message is one NDJSON line on an app connection, in either direction.
//
// Daemon to app: role {active, alone, pid} (pid: the active copy's, sent to a
// standby), alone (this active copy became the only one connected), run {run,
// task, env, params, at} (at: the firing, for a run the schedule started),
// stop {run}, overlap {task, at} (a firing on_overlap = "skip" dropped),
// missed {task, at} (firings missed while the daemon was down, at the latest),
// config_ok {result}, config_error {error, restart}.
// App to daemon: output {run, stream, line, partial} (partial: no newline
// after line), exit {run, code}, config {doc}, close (the app is shutting
// down for good, not restarting; see leave).
type message struct {
	Type    string              `json:"type"`
	Active  *bool               `json:"active,omitempty"`
	Alone   bool                `json:"alone,omitempty"`
	Pid     int                 `json:"pid,omitempty"`
	Run     string              `json:"run,omitempty"`
	Task    string              `json:"task,omitempty"`
	At      *time.Time          `json:"at,omitempty"`
	Env     map[string]string   `json:"env,omitempty"`
	Params  map[string]string   `json:"params,omitempty"`
	Stream  string              `json:"stream,omitempty"`
	Line    string              `json:"line,omitempty"`
	Partial bool                `json:"partial,omitempty"`
	Code    *int                `json:"code,omitempty"`
	Doc     json.RawMessage     `json:"doc,omitempty"`
	Result  *model.ReloadResult `json:"result,omitempty"`
	Error   string              `json:"error,omitempty"`
	Restart bool                `json:"restart,omitempty"`
}

// Host tracks the connected apps and implements executor.Backend for sdk
// units.
type Host struct {
	after        func(time.Duration) <-chan time.Time
	writeTimeout time.Duration

	// config and reload are set by AcceptConfig on a daemon whose config an
	// app supplies; nil otherwise.
	config *config.DocumentSource
	reload func() (model.ReloadResult, error)
	// configMu makes "is the sender active" and applying its document one
	// step, so a copy that just handed over can't have its document land
	// after the next copy's.
	configMu sync.Mutex

	unsubscribe func()

	// services is set by ControlServices; nil leaves an app's services to
	// end however the app ends them.
	services Services
	// servicesMu makes stopping a leaving copy's services and starting them
	// for the next copy one step each, so a copy that connects meanwhile
	// can't miss them.
	servicesMu sync.Mutex
	// parked are the services stopped when the last copy left, for the next
	// copy to connect to start.
	parked []string

	mu       sync.Mutex
	sessions []*session // in connection order; sessions[0] is active
	// leaving are apps that said they are shutting down and are still
	// connected while their runs finish. They count as connected for
	// ExitWhenIdle, so the daemon doesn't stop those runs.
	leaving []*session
	changed chan struct{}
	closed  bool
	// roleSeq numbers the role and alone messages as they are decided, under
	// mu; a session drops one that arrives after a newer one (see sendOrdered).
	roleSeq uint64
	// quit is set once the last app said it is shutting down and then
	// disconnected, so an idle daemon need not wait for it to come back.
	quit bool
	// missed holds the missed messages published while no app was connected,
	// for the next app to become active.
	missed []message
}

// AcceptConfig lets the active app replace the daemon's config. Each document
// it sends is set on src and applied by reload, exactly like an edited
// runwisp.toml followed by `runwisp reload`: validated whole first, rejected
// whole on any error. Call before the first app connects.
func (h *Host) AcceptConfig(src *config.DocumentSource, reload func() (model.ReloadResult, error)) {
	h.config = src
	h.reload = reload
}

// Services is the part of the run manager a Host uses to take an app's sdk
// services off a copy that is shutting down.
type Services interface {
	RestartServiceInstances(name string) error
	StopService(name string) error
	StartService(name string) error
}

// ControlServices lets the Host stop the sdk services of a copy that says it
// is shutting down, so their runs end as stopped, not as failed when the app
// stops them on its way out. Call before the first app connects.
func (h *Host) ControlServices(services Services) {
	h.services = services
}

// ForwardSkippedFirings tells the active app about firings of its tasks that
// never ran, so a task handle can emit node-cron's execution:overlap (a run
// on_overlap = "skip" dropped) and execution:missed (ticks missed while the
// daemon was down). Missed ones are found at boot, before the app that
// started the daemon connects, so they wait for it. The app ignores names it
// doesn't serve.
func (h *Host) ForwardSkippedFirings(bus *events.Bus) {
	h.unsubscribe = bus.Subscribe(events.EventRunFailed, func(e events.Event) {
		re, ok := e.Data.(events.RunEvent)
		if !ok || re.Run == nil || re.Run.EndReason == nil {
			return
		}
		at := re.Run.CreatedAt
		m := message{Task: re.Run.TaskName, At: &at}
		switch *re.Run.EndReason {
		case model.ReasonSkipped:
			m.Type = "overlap"
		case model.ReasonMissed:
			m.Type = "missed"
		default:
			return
		}
		h.mu.Lock()
		var active *session
		if len(h.sessions) > 0 {
			active = h.sessions[0]
		} else if m.Type == "missed" {
			h.missed = append(h.missed, m)
			if len(h.missed) > maxHeldMissed {
				h.missed = h.missed[1:]
			}
		}
		h.mu.Unlock()
		if active == nil {
			return
		}
		// Queued for the session's own goroutine: a slow app must not stall the
		// scheduler, and it hears these in order.
		select {
		case active.notices <- m:
		default:
			slog.Debug("Dropped a firing that didn't run: the app is too slow")
		}
	})
}

// ExitWhenIdle calls exit once no app has been connected for idle, counting
// from now, for a daemon that exists only to serve its app. It exits right
// away when the last app said it is shutting down.
func (h *Host) ExitWhenIdle(idle time.Duration, exit func()) {
	go func() {
		for {
			h.mu.Lock()
			connected, changed, closed, quit := len(h.sessions)+len(h.leaving), h.changed, h.closed, h.quit
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
	return &Host{after: time.After, writeTimeout: defaultWriteTimeout, changed: make(chan struct{})}
}

// ServeHTTP upgrades a request to an app connection and serves it until either
// side closes it. The caller decides who may connect: the daemon mounts this on
// its local socket only.
//
// The query names the app (?app=) and its pid (?pid=). Copies of one app share
// a daemon; a different app is refused while one is connected, so two apps
// pointed at one data directory fail loudly instead of swapping configs.
func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), Protocol) {
		w.Header().Set("Upgrade", Protocol)
		http.Error(w, "expected Upgrade: "+Protocol, http.StatusUpgradeRequired)
		return
	}
	app := r.URL.Query().Get("app")
	pid, _ := strconv.Atoi(r.URL.Query().Get("pid"))
	h.mu.Lock()
	other := h.otherAppLocked(app)
	h.mu.Unlock()
	if other != nil {
		http.Error(w, other.error(), http.StatusConflict)
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
	h.ServeConn(hijacked{conn, buf.Reader}, app, pid)
}

// hijacked reads through the buffer the HTTP server may already have filled,
// and keeps the connection's deadlines for writes.
type hijacked struct {
	net.Conn
	r io.Reader
}

func (c hijacked) Read(p []byte) (int, error) { return c.r.Read(p) }

// ServeConn serves one connection of app (whose process is pid) until it
// closes. Its runs that are still going end as failed when it does.
func (h *Host) ServeConn(conn io.ReadWriteCloser, app string, pid int) {
	s := newSession(conn, app, pid, h.writeTimeout)
	if !h.add(s) {
		_ = conn.Close()
		return
	}
	defer h.remove(s)
	defer close(s.done)
	go s.sendNotices()

	// Configs apply in the order they arrive and are answered in that order:
	// each waits for the one before it. ponytail: unbounded chain, but an app
	// sends a document per config change and waits on each answer.
	configsDone := closedChan

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
			prev, done := configsDone, make(chan struct{})
			configsDone = done
			go func() {
				defer close(done)
				<-prev
				h.applyConfig(s, m.Doc)
			}()
		case "close":
			h.leave(s)
		default:
			s.handle(m)
		}
	}
	switch err := scanner.Err(); {
	case err == nil, errors.Is(err, io.ErrClosedPipe), errors.Is(err, net.ErrClosed):
	case errors.Is(err, bufio.ErrTooLong):
		slog.Warn("App sent a message over the size limit; closing its connection, which fails its runs", "limit_bytes", maxLine)
	default:
		slog.Warn("App connection failed; its runs fail", "err", err)
	}
}

var closedChan = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// Close disconnects every app and refuses new ones. Runs still in flight end
// as failed.
func (h *Host) Close() {
	if h.unsubscribe != nil {
		h.unsubscribe()
	}
	h.mu.Lock()
	h.closed = true
	sessions := slices.Concat(h.sessions, h.leaving)
	h.broadcastLocked()
	h.mu.Unlock()
	for _, s := range sessions {
		_ = s.conn.Close()
	}
}

// otherAppLocked returns a connected session of an app other than app, if any.
func (h *Host) otherAppLocked(app string) *session {
	for _, s := range slices.Concat(h.sessions, h.leaving) {
		if s.app != app {
			return s
		}
	}
	return nil
}

func (h *Host) add(s *session) bool {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return false
	}
	// Rechecked here: ServeHTTP's check and this one aren't one step.
	if other := h.otherAppLocked(s.app); other != nil {
		h.mu.Unlock()
		slog.Warn("Refused an app connection", "err", other.error())
		return false
	}
	h.sessions = append(h.sessions, s)
	h.roleSeq++
	seq := h.roleSeq
	h.quit = false
	active := h.sessions[0]
	connected := len(h.sessions)
	alone := h.aloneLocked()
	var missed []message
	if active == s {
		missed, h.missed = h.missed, nil
	}
	h.broadcastLocked()
	h.mu.Unlock()

	s.sendRole(seq, active == s, alone, active.pid)
	for _, m := range missed {
		if err := s.send(m); err != nil {
			slog.Debug("Failed to tell an app about a missed firing", "err", err)
		}
	}
	if active == s {
		h.resumeServices()
	}
	slog.Info("App connected", "app", s.app, "pid", s.pid, "active", active == s, "connected", connected)
	return true
}

// aloneLocked reports whether the active app is the only one connected.
func (h *Host) aloneLocked() bool {
	return len(h.sessions) == 1 && len(h.leaving) == 0
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
	h.leaving = slices.DeleteFunc(h.leaving, func(other *session) bool { return other == s })
	connected := len(h.sessions) + len(h.leaving)
	if s.last && connected == 0 {
		h.quit = true
	}
	// The active copy hears it is alone once the others are gone: then it can
	// restart the daemon without stopping another copy's runs.
	var alone *session
	if next == nil && h.aloneLocked() && h.sessions[0] != s {
		alone = h.sessions[0]
	}
	nextAlone := h.aloneLocked()
	h.roleSeq++
	seq := h.roleSeq
	h.broadcastLocked()
	h.mu.Unlock()

	_ = s.conn.Close()
	s.disconnect()
	if next != nil {
		next.sendRole(seq, true, nextAlone, 0)
	}
	if alone != nil {
		if err := alone.sendOrdered(seq, message{Type: "alone"}); err != nil {
			slog.Debug("Failed to tell an app it is alone", "err", err)
		}
	}
	slog.Info("App disconnected", "app", s.app, "pid", s.pid, "connected", connected)
}

// leave takes an app that is shutting down out of service. Its runs keep
// going until they end, and the daemon stays up for them. When other copies
// are connected, the next one takes over and the app is told it is on
// standby. When it was the last, the daemon may exit right away
// (ExitWhenIdle) once the app disconnects, which it does after its runs end.
func (h *Host) leave(s *session) {
	h.mu.Lock()
	if s.leaving {
		h.mu.Unlock()
		return
	}
	s.leaving = true
	next := h.detachLocked(s)
	h.leaving = append(h.leaving, s)
	last := len(h.sessions) == 0
	s.last = last
	var activePid int
	if !last {
		activePid = h.sessions[0].pid
	}
	h.roleSeq++
	seq := h.roleSeq
	h.mu.Unlock()

	if !last {
		s.sendRole(seq, false, false, activePid)
	}
	if next != nil {
		next.sendRole(seq, true, false, 0)
	}
	h.releaseServices(s.services(), last)
}

// releaseServices stops the services a leaving copy runs. Called before the
// read loop reads on, so the stop comes before the exits the app sends as it
// stops them itself. With another copy connected they restart on it right
// away; after the last copy they stay down until the next one connects.
func (h *Host) releaseServices(names []string, last bool) {
	if h.services == nil || len(names) == 0 {
		return
	}
	h.servicesMu.Lock()
	defer h.servicesMu.Unlock()
	for _, name := range names {
		var err error
		if last {
			err = h.services.StopService(name)
		} else {
			err = h.services.RestartServiceInstances(name)
		}
		if err != nil {
			slog.Warn("Failed to stop a service of an app that is shutting down", "task", name, "err", err)
		}
	}
	if !last {
		return
	}
	h.parked = append(h.parked, names...)
	h.mu.Lock()
	connected := len(h.sessions) > 0
	h.mu.Unlock()
	if connected {
		// A copy connected after the last one left and before its services
		// were parked: they're its to run.
		h.resumeServicesLocked()
	}
}

// resumeServices starts the services parked when the last copy left, for the
// copy that just became active.
func (h *Host) resumeServices() {
	h.servicesMu.Lock()
	defer h.servicesMu.Unlock()
	h.resumeServicesLocked()
}

func (h *Host) resumeServicesLocked() {
	for _, name := range h.parked {
		if err := h.services.StartService(name); err != nil {
			slog.Debug("Failed to start a service for the app that connected", "task", name, "err", err)
		}
	}
	h.parked = nil
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
// Documents apply one at a time, each only if its sender is still active.
func (h *Host) applyConfig(s *session, doc json.RawMessage) {
	if err := s.send(h.configReply(s, doc)); err != nil {
		slog.Debug("Failed to answer an app's config", "err", err)
	}
}

// configReply applies doc if s is still the active copy and returns the answer.
func (h *Host) configReply(s *session, doc json.RawMessage) message {
	reply := message{Type: "config_error"}
	if h.reload == nil {
		reply.Error = "this daemon reads its config from runwisp.toml, not from an app"
		return reply
	}
	h.configMu.Lock()
	defer h.configMu.Unlock()
	if !h.isActive(s) {
		reply.Error = "this copy of the app is on standby; the active copy's config applies"
		return reply
	}
	h.config.Set(doc)
	result, err := h.reload()
	if err != nil {
		reply.Error = err.Error()
		reply.Restart = errors.Is(err, runtime.ErrRestartRequired)
		return reply
	}
	return message{Type: "config_ok", Result: &result}
}

func (h *Host) isActive(s *session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions) > 0 && h.sessions[0] == s
}

// session is one connected app.
type session struct {
	conn io.ReadWriteCloser
	app  string
	pid  int

	// wmu orders writes. seq is the newest role or alone message written.
	wmu          sync.Mutex
	enc          *json.Encoder
	writeTimeout time.Duration
	seq          uint64

	// notices holds overlap and missed messages for sendNotices to write.
	notices chan message
	done    chan struct{} // closed when the connection is over

	mu   sync.Mutex
	runs map[string]*handlerRun
	gone bool

	// leaving is set, under Host.mu, once the app said it is shutting down;
	// last when it was the last copy then.
	leaving, last bool
}

func newSession(conn io.ReadWriteCloser, app string, pid int, writeTimeout time.Duration) *session {
	return &session{
		conn: conn, app: app, pid: pid, enc: json.NewEncoder(conn), writeTimeout: writeTimeout,
		notices: make(chan message, maxQueuedNotices), done: make(chan struct{}),
		runs: map[string]*handlerRun{},
	}
}

// error says why another app can't connect while this one is.
func (s *session) error() string {
	return fmt.Sprintf("another app (%q, pid %d) uses this RunWisp daemon; give each app its own data directory", s.app, s.pid)
}

func (s *session) send(m message) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.writeLocked(m)
}

// sendOrdered sends a role or alone message numbered seq when Host decided it
// (under its lock). One that a newer decision already overtook is stale and is
// dropped, so the app's last role is the daemon's latest.
func (s *session) sendOrdered(seq uint64, m message) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if seq < s.seq {
		return nil
	}
	s.seq = seq
	return s.writeLocked(m)
}

// writeLocked writes m within the write timeout. A write that fails or times
// out leaves the stream unusable, so the connection is closed: the read loop
// ends and the app's runs fail.
func (s *session) writeLocked(m message) error {
	if c, ok := s.conn.(interface{ SetWriteDeadline(time.Time) error }); ok && s.writeTimeout > 0 {
		_ = c.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	}
	err := s.enc.Encode(m)
	if err != nil {
		_ = s.conn.Close()
	}
	return err
}

// sendNotices writes the queued overlap and missed messages until the
// connection is over.
func (s *session) sendNotices() {
	for {
		select {
		case m := <-s.notices:
			if err := s.send(m); err != nil {
				slog.Debug("Failed to tell an app about a firing that didn't run", "err", err)
			}
		case <-s.done:
			return
		}
	}
}

// sendRole tells the app whether it is active and, if so, whether it is the
// only app connected; a standby hears the active copy's pid.
func (s *session) sendRole(seq uint64, active, alone bool, activePid int) {
	m := message{Type: "role", Active: &active, Alone: alone && active}
	if !active {
		m.Pid = activePid
	}
	if err := s.sendOrdered(seq, m); err != nil {
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
		if !m.Partial {
			m.Line += "\n"
		}
		_, _ = io.WriteString(w, m.Line)
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

// services returns the names of the services the app is running, once each.
func (s *session) services() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for _, r := range s.runs {
		if r.service && !slices.Contains(names, r.task) {
			names = append(names, r.task)
		}
	}
	return names
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
