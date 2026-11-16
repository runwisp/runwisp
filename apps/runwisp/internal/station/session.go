// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
)

const (
	// maxInboundFrameBytes caps one frame from the station.
	maxInboundFrameBytes int64 = 16 << 20
	writeTimeout               = 10 * time.Second
	outboundQueueSize          = 256
	// Session parameters used when welcome leaves them out.
	defaultHeartbeatInterval = 15 * time.Second
	defaultHeartbeatTimeout  = 45 * time.Second
	defaultMaxFrameBytes     = 4 << 20
	// minMaxFrameBytes keeps a log:lines batch able to carry a line.
	minMaxFrameBytes = 64 << 10
	// goodbyeTimeout bounds the goodbye the daemon sends on shutdown.
	goodbyeTimeout = time.Second
)

var (
	errSessionClosed    = &StationError{Code: protocol.ErrorCodeUnavailable, Message: "station session closed", Retryable: true}
	errHeartbeatTimeout = errors.New("heartbeat timeout: nothing heard from the station")
	errReconfigured     = errors.New("session parameters changed")
)

// sessionParams are the welcome's knobs, changeable by session:update.
type sessionParams struct {
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	statsInterval     time.Duration
	maxFrameBytes     int
}

func paramsFromWelcome(w protocol.Welcome) sessionParams {
	p := sessionParams{
		heartbeatInterval: defaultHeartbeatInterval,
		heartbeatTimeout:  defaultHeartbeatTimeout,
		statsInterval:     time.Duration(max(w.StatsIntervalMs, 0)) * time.Millisecond,
		maxFrameBytes:     defaultMaxFrameBytes,
	}
	p.applyHeartbeat(w.Heartbeat)
	if w.MaxFrameBytes > 0 {
		p.maxFrameBytes = max(w.MaxFrameBytes, minMaxFrameBytes)
	}
	return p
}

func (p *sessionParams) applyHeartbeat(hb *protocol.Heartbeat) {
	if hb == nil {
		return
	}
	if hb.IntervalMs > 0 {
		p.heartbeatInterval = time.Duration(hb.IntervalMs) * time.Millisecond
	}
	if hb.TimeoutMs > 0 {
		p.heartbeatTimeout = time.Duration(hb.TimeoutMs) * time.Millisecond
	}
}

type outFrame struct {
	data []byte
	// written, when set, receives the write's outcome.
	written chan error
}

// session is one accepted connection: from welcome until either side closes.
type session struct {
	client *Client
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelCauseFunc
	out    chan outFrame
	// helloHash is the catalog hash hello reported.
	helloHash    string
	lastReceived atomic.Int64
	nextID       atomic.Uint64

	mu          sync.Mutex
	params      sessionParams
	reconfigure chan struct{}
	pending     map[string]chan protocol.Envelope // outgoing commands by id
	subs        map[string]context.CancelFunc     // log subscriptions by command id
	advertised  advertisedAgent                   // what hello / agent:update last said
	dirty       map[string]struct{}               // services whose status changed
	servicesDue chan struct{}
}

// advertisedAgent is the part of hello that agent:update can change.
type advertisedAgent struct {
	agent    protocol.AgentInfo
	features map[string]*protocol.FeatureStatus
}

func newSession(parent context.Context, client *Client, conn *websocket.Conn, welcome protocol.Welcome, hello protocol.Hello) *session {
	// Not cancelled with parent: a cancelled read closes the socket, and on
	// shutdown the goodbye must go out first. run ends the session itself.
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	s := &session{
		client:      client,
		conn:        conn,
		ctx:         ctx,
		cancel:      cancel,
		out:         make(chan outFrame, outboundQueueSize),
		helloHash:   hello.TasksHash,
		params:      paramsFromWelcome(welcome),
		reconfigure: make(chan struct{}, 1),
		pending:     map[string]chan protocol.Envelope{},
		subs:        map[string]context.CancelFunc{},
		advertised:  advertisedAgent{agent: *hello.Agent, features: hello.Features},
		dirty:       map[string]struct{}{},
		servicesDue: make(chan struct{}, 1),
	}
	s.lastReceived.Store(time.Now().UnixMilli())
	return s
}

// run drives the session until it ends and returns why. The session is
// attached (live sends reach it) only while its loops run.
func (s *session) run(parent context.Context) error {
	var wg sync.WaitGroup
	loop := func(fn func() error) {
		wg.Go(func() {
			defer crashguard.Guard()
			if err := fn(); err != nil {
				s.cancel(err)
			}
		})
	}
	loop(s.readLoop)
	loop(s.writeLoop)
	loop(s.timersLoop)
	loop(s.outboxLoop)
	loop(s.servicesLoop)

	s.client.attach(s)
	slog.Info("station session started")
	go s.afterWelcome()

	select {
	case <-s.ctx.Done():
	case <-parent.Done():
		s.sayGoodbye()
		s.cancel(context.Canceled)
	}
	cause := context.Cause(s.ctx)
	s.client.detach(s)
	_ = s.conn.Close(websocket.StatusNormalClosure, "")
	wg.Wait()

	if parent.Err() != nil {
		return nil
	}
	if status := websocket.CloseStatus(cause); status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
		// Never silent: an idle-kill loop on the station side looks like this.
		slog.Warn("station closed the session", "status", status.String())
		return nil
	}
	return cause
}

// afterWelcome does the post-welcome work that needs the loops running:
// tasks:sync when the station doesn't know the catalog (or a reload changed
// it since hello), agent:update if a reload changed features since hello,
// then the archives still waiting for upload targets.
func (s *session) afterWelcome() {
	defer crashguard.Guard()
	s.sendAgentUpdateIfChanged()
	s.client.syncTasks(s, false)
	s.client.uploader.RetryPending(s.ctx, s.client.tracker.Known, s.client.outbox.append)
}

// sayGoodbye tells the station the daemon is shutting down, so it marks the
// runner offline at once instead of waiting out the heartbeat.
func (s *session) sayGoodbye() {
	frame, err := encodeFrame(typeGoodbye, "", "", protocol.Goodbye{Code: protocol.GoodbyeCodeShutdown, Message: "daemon shutting down", Retry: true})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), goodbyeTimeout)
	defer cancel()
	_ = s.conn.Write(ctx, websocket.MessageText, frame)
}

func (s *session) readLoop() error {
	for {
		_, payload, err := s.conn.Read(s.ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		s.lastReceived.Store(time.Now().UnixMilli())
		if err := s.handleFrame(payload); err != nil {
			return err
		}
	}
}

func (s *session) writeLoop() error {
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case f := <-s.out:
			ctx, cancel := context.WithTimeout(s.ctx, writeTimeout)
			err := s.conn.Write(ctx, websocket.MessageText, f.data)
			cancel()
			if f.written != nil {
				f.written <- err
			}
			if err != nil {
				return fmt.Errorf("write: %w", err)
			}
		}
	}
}

// timersLoop sends pings and agent:stats and closes a silent session. It
// restarts its tickers when session:update changes the parameters.
func (s *session) timersLoop() error {
	for {
		if err := s.runTimers(s.currentParams()); !errors.Is(err, errReconfigured) {
			return err
		}
	}
}

func (s *session) runTimers(p sessionParams) error {
	ping := time.NewTicker(p.heartbeatInterval)
	defer ping.Stop()
	watchdog := time.NewTicker(min(p.heartbeatTimeout/4, 2*time.Second))
	defer watchdog.Stop()
	var stats <-chan time.Time
	if p.statsInterval > 0 && s.client.deps.SystemStats != nil {
		t := time.NewTicker(p.statsInterval)
		defer t.Stop()
		stats = t.C
	}
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-s.reconfigure:
			return errReconfigured
		case <-ping.C:
			// A dropped ping is harmless: a full queue means the station is
			// hearing from us anyway.
			s.trySend(typePing, nil)
		case <-stats:
			s.trySend(typeAgentStats, toAgentStats(s.client.deps.SystemStats(), time.Since(s.client.startedAt)))
		case <-watchdog.C:
			if time.Since(time.UnixMilli(s.lastReceived.Load())) > p.heartbeatTimeout {
				return errHeartbeatTimeout
			}
		}
	}
}

// outboxLoop streams the outbox in seq order: everything unacked right after
// welcome, then each update as it is stored.
func (s *session) outboxLoop() error {
	var after int64
	for {
		updates, next, err := s.client.outbox.read(s.ctx, after)
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			slog.Warn("failed to read execution updates to send", "err", err)
		}
		for _, u := range updates {
			if err := s.send(typeExecUpdate, "", "", u, false); err != nil {
				return nil
			}
		}
		if next > after {
			after = next
			continue
		}
		select {
		case <-s.ctx.Done():
			return nil
		case <-s.client.outbox.wake:
		case <-time.After(time.Minute): // a missed wake costs at most this
		}
	}
}

// servicesLoop sends every service's status after welcome, then the latest
// status of each service that changed. Snapshots coalesce, so a burst of
// lifecycle events sends one frame per service.
func (s *session) servicesLoop() error {
	for _, svc := range s.client.deps.TaskManager.ListServiceTasks() {
		if svc != nil {
			s.serviceChanged(svc.Name)
		}
	}
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-s.servicesDue:
		}
		s.mu.Lock()
		names := s.dirty
		s.dirty = map[string]struct{}{}
		s.mu.Unlock()
		for name := range maps.Keys(names) {
			snapshot, ok := s.client.deps.TaskManager.ServiceSnapshot(name)
			if !ok {
				continue
			}
			if err := s.send(typeServiceStatus, "", "", newServiceStatus(snapshot), false); err != nil {
				return nil
			}
		}
	}
}

func (s *session) serviceChanged(name string) {
	s.mu.Lock()
	s.dirty[name] = struct{}{}
	s.mu.Unlock()
	select {
	case s.servicesDue <- struct{}{}:
	default:
	}
}

func (s *session) currentParams() sessionParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.params
}

func (s *session) maxFrameBytes() int {
	return s.currentParams().maxFrameBytes
}

// updateParams applies a session:update.
func (s *session) updateParams(u protocol.SessionUpdate) {
	s.mu.Lock()
	s.params.applyHeartbeat(u.Heartbeat)
	if u.StatsIntervalMs != nil {
		s.params.statsInterval = time.Duration(max(*u.StatsIntervalMs, 0)) * time.Millisecond
	}
	s.mu.Unlock()
	select {
	case s.reconfigure <- struct{}{}:
	default:
	}
}

// handleFrame acts on one inbound frame. It is the funnel every peer frame
// passes through, so the panic guard lives here: the read loop runs on its own
// goroutine, and an unrecovered panic would take down the whole daemon over
// one malformed frame from an optional integration. A non-nil error ends the
// session.
func (s *session) handleFrame(payload []byte) (err error) {
	var env protocol.Envelope
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("recovered from panic handling a station frame", "type", env.Type, "err", rec, "stack", string(debug.Stack()))
			if env.ID != "" {
				s.reply(env.ID, nil, newError(protocol.ErrorCodeInternal, "the daemon failed to process this command"), false)
			}
		}
	}()
	if err := json.Unmarshal(payload, &env); err != nil {
		slog.Info("ignoring malformed station frame", "error", err.Error())
		return nil
	}

	switch env.Type {
	case typePong:
		return nil
	case typeResult:
		s.deliverResult(env)
		return nil
	case typeAck:
		if ack, err := decodeData[protocol.Ack](env); err == nil {
			s.client.outbox.ack(ack.Seq)
		}
		return nil
	case typeGoodbye:
		g, err := decodeData[protocol.Goodbye](env)
		if err != nil {
			return err
		}
		return &goodbyeError{g}
	case typeLogUnsubscribe:
		u, err := decodeData[protocol.LogUnsubscribe](env)
		if err == nil {
			s.unsubscribe(u.Subscription)
		}
		if env.ID != "" {
			s.reply(env.ID, nil, err, false)
		}
		return nil
	}
	if env.ID == "" {
		// Forward-compat: a non-command type this daemon doesn't know.
		slog.Debug("ignoring station frame", "type", env.Type)
		return nil
	}
	s.runCommand(env)
	return nil
}

// runCommand answers one command with exactly one result. An unknown command,
// or one requiring a feature this daemon lacks or can't use now, is refused
// with unsupported before anything happens.
func (s *session) runCommand(env protocol.Envelope) {
	cmd, known := commands[env.Type]
	if !known {
		s.reply(env.ID, nil, newError(protocol.ErrorCodeUnsupported, "unknown command %q", env.Type), false)
		return
	}
	if missing := s.client.missingFeatures(env.Requires); missing != "" {
		s.reply(env.ID, nil, newError(protocol.ErrorCodeUnsupported, "%s", missing), false)
		return
	}
	exec := func() {
		defer crashguard.Guard()
		result, err := cmd.run(s.ctx, s, env)
		then, hasThen := result.(followUp)
		if hasThen {
			result = then.result
		}
		s.reply(env.ID, result, err, hasThen)
		if hasThen && err == nil && then.then != nil {
			then.then()
		}
	}
	if cmd.async {
		go exec()
		return
	}
	exec()
}

// followUp is a command result with work to do once the result is written,
// such as restarting the daemon.
type followUp struct {
	result any
	then   func()
}

// reply sends a command's result. waitWritten blocks until the frame is on
// the wire.
func (s *session) reply(id string, fields any, err error, waitWritten bool) {
	data, mErr := resultData(fields, err)
	if mErr != nil {
		data, _ = resultData(nil, newError(protocol.ErrorCodeInternal, "encode result: %v", mErr))
	}
	if sendErr := s.send(typeResult, "", id, data, waitWritten); sendErr != nil {
		slog.Debug("failed to send command result", "replyTo", id, "error", sendErr.Error())
	}
}

// send queues a frame, waiting while the outbound queue is full.
func (s *session) send(typ, id, replyTo string, data any, waitWritten bool) error {
	frame, err := encodeFrame(typ, id, replyTo, data)
	if err != nil {
		return err
	}
	f := outFrame{data: frame}
	if waitWritten {
		f.written = make(chan error, 1)
	}
	select {
	case s.out <- f:
	case <-s.ctx.Done():
		return errSessionClosed
	}
	if !waitWritten {
		return nil
	}
	select {
	case err := <-f.written:
		return err
	case <-s.ctx.Done():
		return errSessionClosed
	}
}

// trySend queues a frame unless the outbound queue is full.
func (s *session) trySend(typ string, data any) bool {
	if s.ctx.Err() != nil {
		return false
	}
	frame, err := encodeFrame(typ, "", "", data)
	if err != nil {
		return false
	}
	select {
	case s.out <- outFrame{data: frame}:
		return true
	default:
		return false
	}
}

// command sends a daemon command and waits for its result.
func (s *session) command(ctx context.Context, typ string, data any) (protocol.Envelope, error) {
	id := "d" + strconv.FormatUint(s.nextID.Add(1), 10)
	ch := make(chan protocol.Envelope, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()
	if err := s.send(typ, id, "", data, false); err != nil {
		return protocol.Envelope{}, err
	}
	select {
	case env := <-ch:
		return env, nil
	case <-ctx.Done():
		return protocol.Envelope{}, &StationError{Code: protocol.ErrorCodeInternal, Message: typ + " got no result", Retryable: true, Err: ctx.Err()}
	case <-s.ctx.Done():
		return protocol.Envelope{}, errSessionClosed
	}
}

func (s *session) deliverResult(env protocol.Envelope) {
	s.mu.Lock()
	ch, ok := s.pending[env.ReplyTo]
	s.mu.Unlock()
	if !ok {
		slog.Debug("ignoring result for an unknown command", "replyTo", env.ReplyTo)
		return
	}
	select {
	case ch <- env:
	default:
	}
}

// decodeResult turns a result into T, or into the StationError it carries.
func decodeResult[T any](env protocol.Envelope) (T, error) {
	var zero T
	res, err := decodeData[protocol.Result](env)
	if err != nil {
		return zero, err
	}
	if !res.Ok {
		info := res.Error
		if info == nil {
			info = &protocol.ErrorInfo{Code: protocol.ErrorCodeInternal, Message: "station refused without a reason"}
		}
		return zero, &StationError{Code: info.Code, Message: info.Message, Retryable: info.Retryable}
	}
	return decodeData[T](env)
}

// sendAgentUpdateIfChanged reports features or identity that changed since
// hello (a reload added the first TOML task, renamed the daemon).
func (s *session) sendAgentUpdateIfChanged() {
	now := advertisedAgent{agent: *s.client.agentInfo(), features: s.client.features()}
	s.mu.Lock()
	update := protocol.AgentUpdate{}
	changed := false
	if now.agent != s.advertised.agent {
		update.Agent, changed = &now.agent, true
	}
	if !maps.EqualFunc(now.features, s.advertised.features, func(a, b *protocol.FeatureStatus) bool { return *a == *b }) {
		update.Features, changed = now.features, true
	}
	s.advertised = now
	s.mu.Unlock()
	if changed {
		_ = s.send(typeAgentUpdate, "", "", update, false)
	}
}
