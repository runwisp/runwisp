// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package stationtest is a scripted station for tests of the daemon's
// station client: it accepts the daemon's WebSocket, answers hello with
// welcome, ping with pong and every daemon command with a result, and records
// every frame the daemon sends.
package stationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
)

// WaitTimeout bounds every wait for a daemon frame.
const WaitTimeout = 5 * time.Second

// Station is one scripted station. Set its fields before the daemon dials.
type Station struct {
	URL *url.URL
	// Welcome is sent in answer to every hello.
	Welcome protocol.Welcome
	// Goodbye, when set, answers hello instead of Welcome, then closes.
	Goodbye *protocol.Goodbye
	// NoSubprotocol accepts the WebSocket without selecting runwisp.v1, like
	// a station that predates it.
	NoSubprotocol bool
	// AutoAck acks every execution:update as it arrives.
	AutoAck bool
	// Answer returns the result fields (or the error) for a daemon command.
	// nil answers ok with no fields.
	Answer func(env protocol.Envelope) (fields any, err *protocol.ErrorInfo)

	srv *httptest.Server

	mu       sync.Mutex
	conn     *websocket.Conn
	frames   []protocol.Envelope
	consumed []bool
	changed  chan struct{}
	tokens   []string
	ids      int
}

// New starts a station; it stops when the test ends.
func New(t testing.TB) *Station {
	s := &Station{
		Welcome: protocol.Welcome{
			ConnectionID:   "conn-1",
			Features:       []string{},
			Heartbeat:      &protocol.Heartbeat{IntervalMs: 15000, TimeoutMs: 45000},
			MaxFrameBytes:  4 << 20,
			TasksHashKnown: true,
		},
		changed: make(chan struct{}),
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	s.URL, _ = url.Parse(s.srv.URL)
	return s
}

func (s *Station) serve(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{}
	if !s.NoSubprotocol {
		opts.Subprotocols = []string{"runwisp.v1"}
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	conn.SetReadLimit(64 << 20)
	s.mu.Lock()
	s.tokens = append(s.tokens, r.Header.Get("Authorization"))
	s.conn = conn
	s.mu.Unlock()
	defer conn.CloseNow()

	ctx := r.Context()
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var env protocol.Envelope
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		// React first: once a test sees hello, the welcome is already on the
		// wire, so a command it sends next can't overtake it.
		keep := s.react(ctx, conn, env)
		s.record(env)
		if !keep {
			return
		}
	}
}

// react plays the station's side of one frame; false closes the connection.
func (s *Station) react(ctx context.Context, conn *websocket.Conn, env protocol.Envelope) bool {
	switch {
	case env.Type == "hello" && s.Goodbye != nil:
		_ = s.write(ctx, conn, "goodbye", "", "", s.Goodbye)
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return false
	case env.Type == "hello":
		_ = s.write(ctx, conn, "welcome", "", "", s.Welcome)
	case env.Type == "ping":
		_ = s.write(ctx, conn, "pong", "", "", nil)
	case env.Type == "execution:update" && s.AutoAck:
		var u protocol.ExecutionUpdate
		_ = json.Unmarshal(env.Data, &u)
		_ = s.write(ctx, conn, "ack", "", "", protocol.Ack{Seq: u.Seq})
	case env.ID != "":
		_ = s.write(ctx, conn, "result", "", env.ID, s.result(env))
	}
	return true
}

func (s *Station) result(env protocol.Envelope) json.RawMessage {
	var fields any
	var failure *protocol.ErrorInfo
	if s.Answer != nil {
		fields, failure = s.Answer(env)
	}
	if failure != nil {
		raw, _ := json.Marshal(protocol.Result{Error: failure})
		return raw
	}
	obj := map[string]any{}
	if fields != nil {
		raw, _ := json.Marshal(fields)
		_ = json.Unmarshal(raw, &obj)
	}
	obj["ok"] = true
	raw, _ := json.Marshal(obj)
	return raw
}

func (s *Station) write(ctx context.Context, conn *websocket.Conn, typ, id, replyTo string, data any) error {
	env := protocol.Envelope{Type: typ, ID: id, ReplyTo: replyTo}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		env.Data = raw
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return conn.Write(ctx, websocket.MessageText, raw)
}

func (s *Station) record(env protocol.Envelope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, env)
	s.consumed = append(s.consumed, false)
	close(s.changed)
	s.changed = make(chan struct{})
}

// Send sends a frame to the connected daemon.
func (s *Station) Send(t testing.TB, typ, id string, data any) {
	t.Helper()
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		t.Fatalf("stationtest: no daemon connected to send %s to", typ)
	}
	if err := s.write(t.Context(), conn, typ, id, "", data); err != nil {
		t.Fatalf("stationtest: send %s: %v", typ, err)
	}
}

// Command sends a command with requires and returns its id.
func (s *Station) Command(t testing.TB, typ string, data any, requires ...string) string {
	t.Helper()
	s.mu.Lock()
	s.ids++
	id := "s" + strconv.Itoa(s.ids)
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		t.Fatalf("stationtest: no daemon connected to send %s to", typ)
	}
	env := protocol.Envelope{Type: typ, ID: id, Requires: requires}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	env.Data = raw
	frame, _ := json.Marshal(env)
	s.mu.Lock()
	err = conn.Write(t.Context(), websocket.MessageText, frame)
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("stationtest: send %s: %v", typ, err)
	}
	return id
}

// Result waits for the result of command id and decodes it into out (which
// may be nil); it returns the result's ok and error.
func (s *Station) Result(t testing.TB, id string, out any) protocol.Result {
	t.Helper()
	env := s.Wait(t, "result", func(e protocol.Envelope) bool { return e.ReplyTo == id })
	var res protocol.Result
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("stationtest: malformed result: %v", err)
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			t.Fatalf("stationtest: decode result: %v", err)
		}
	}
	return res
}

// Wait returns the oldest frame of type typ not returned before that match
// accepts (nil accepts all), waiting up to WaitTimeout for it.
func (s *Station) Wait(t testing.TB, typ string, match func(protocol.Envelope) bool) protocol.Envelope {
	t.Helper()
	deadline := time.After(WaitTimeout)
	for {
		s.mu.Lock()
		for i, env := range s.frames {
			if !s.consumed[i] && env.Type == typ && (match == nil || match(env)) {
				s.consumed[i] = true
				s.mu.Unlock()
				return env
			}
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("stationtest: no %s frame within %s; got %s", typ, WaitTimeout, s.Types())
		}
	}
}

// Decode waits for a frame like Wait and decodes its data into T.
func Decode[T any](t testing.TB, s *Station, typ string, match func(T) bool) T {
	t.Helper()
	var out T
	s.Wait(t, typ, func(env protocol.Envelope) bool {
		var v T
		if json.Unmarshal(env.Data, &v) != nil {
			return false
		}
		if match != nil && !match(v) {
			return false
		}
		out = v
		return true
	})
	return out
}

// Types lists the types of every frame received so far, for failure output.
func (s *Station) Types() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := make([]string, len(s.frames))
	for i, env := range s.frames {
		types[i] = env.Type
	}
	return fmt.Sprint(types)
}

// Count reports how many frames of type typ arrived so far.
func (s *Station) Count(typ string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, env := range s.frames {
		if env.Type == typ {
			n++
		}
	}
	return n
}

// Tokens returns the Authorization header of every connection so far.
func (s *Station) Tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tokens)
}

// Drop closes the daemon's connection abruptly.
func (s *Station) Drop() {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.CloseNow()
	}
}
