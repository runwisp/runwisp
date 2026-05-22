// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runwisp/runwisp/internal/events"
)

// Service owns the entire notification subsystem: bus subscription, ingress
// channel, dispatch goroutine, per-action workers, and the in-app pipeline
// (Coalescer + Hub). It is constructed once at daemon startup and stopped
// once at shutdown. A future SIGHUP-driven reload would simply call Stop and
// then construct a fresh Service — no shared state survives the call.
type Service struct {
	bus         events.EventBus
	ingressCh   chan *Event
	ingressSize int

	router   *Router
	disp     *dispatcher
	channels []Channel
	failures SyntheticIngester

	clock  Clocker
	logger *slog.Logger

	retentionEvery time.Duration
	retentionFn    func(context.Context)

	unsubscribe     func()
	cancel          context.CancelFunc // cancels workCtx — workers + runDispatch
	retentionCancel context.CancelFunc // cancels the retention loop; fired upfront in Stop so it never gates wg.Wait
	wg              sync.WaitGroup

	droppedIngress atomic.Uint64
	started        atomic.Bool
	stopped        atomic.Bool
}

// DefaultActionQueueSize is the per-action worker queue capacity and also the
// size of the bus → service ingress buffer. Held internal by design —
// operators don't tune these.
const DefaultActionQueueSize = 256

// Config bundles everything Service.New needs that isn't already on the
// dispatcher / router.
type Config struct {
	Bus            events.EventBus
	Channels       []Channel             // includes inapp + each configured provider
	Rules          []Rule                // routing predicates
	FailureSink    SyntheticIngester     // typically the inapp.Channel
	Clock          Clocker               // 0 → RealClock
	Logger         *slog.Logger          // 0 → slog.Default
	RetentionEvery time.Duration         // 0 → 5min
	RetentionFn    func(context.Context) // executed on each tick; injected by Service builder
}

// New constructs a Service from already-built channels and pre-compiled rules.
// It does not touch any I/O — all start-time work happens in Start.
func New(cfg Config) *Service {
	ingressSize := DefaultActionQueueSize
	actionQueueSize := DefaultActionQueueSize
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	retentionEvery := cfg.RetentionEvery
	if retentionEvery <= 0 {
		retentionEvery = 5 * time.Minute
	}

	channelByID := make(map[string]Channel, len(cfg.Channels))
	for _, c := range cfg.Channels {
		channelByID[c.ID()] = c
	}
	router := NewRouter(cfg.Rules, channelByID)
	disp := newDispatcher(router, channelByID, actionQueueSize, clock, cfg.FailureSink, logger)

	return &Service{
		bus:            cfg.Bus,
		ingressCh:      make(chan *Event, ingressSize),
		ingressSize:    ingressSize,
		router:         router,
		disp:           disp,
		channels:       cfg.Channels,
		failures:       cfg.FailureSink,
		clock:          clock,
		logger:         logger,
		retentionEvery: retentionEvery,
		retentionFn:    cfg.RetentionFn,
	}
}

// Start subscribes to the event bus, launches the dispatch goroutine and the
// per-action workers, and (if configured) the retention ticker. Idempotent:
// repeated Start calls are no-ops.
func (s *Service) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return nil
	}
	if s.bus == nil {
		s.logger.Warn("notify: started without an event bus; running in stopped state")
		return nil
	}

	workCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.disp.startWorkers(workCtx)

	s.wg.Add(1)
	go s.runDispatch(workCtx)

	if s.retentionFn != nil {
		// Retention runs on its own cancellable context so Stop can shut it
		// down immediately without also tearing down the dispatch/worker
		// drain path. Sharing workCtx here used to deadlock Stop: the
		// retention ticker had no exit signal in the happy path, so wg.Wait
		// blocked until the caller-supplied deadline fired.
		retentionCtx, retentionCancel := context.WithCancel(ctx)
		s.retentionCancel = retentionCancel
		s.wg.Add(1)
		go s.runRetention(retentionCtx)
	}

	s.unsubscribe = s.bus.SubscribeAll(s.onBusEvent)
	s.logger.Info("notify started",
		"channels", len(s.channels),
		"rules", len(s.router.rules),
		"ingress_size", s.ingressSize)
	return nil
}

// Stop tears the service down in this order: detach from bus, drain ingress,
// close per-action queues, wait for workers, close all channels. Honors the
// supplied context as a deadline; goroutines are also cancelled directly so
// stuck workers exit even if ctx is the background.
func (s *Service) Stop(ctx context.Context) error {
	if !s.started.Load() {
		return nil
	}
	if !s.stopped.CompareAndSwap(false, true) {
		return nil
	}

	if s.unsubscribe != nil {
		s.unsubscribe()
		s.unsubscribe = nil
	}

	// Retention has nothing to drain — stop it before waiting on wg so it
	// doesn't gate Stop's bounded wait.
	if s.retentionCancel != nil {
		s.retentionCancel()
		s.retentionCancel = nil
	}

	close(s.ingressCh)

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		s.disp.closeQueues()
		s.disp.waitWorkers()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		s.logger.Warn("notify: stop deadline reached; cancelling workers")
		if s.cancel != nil {
			s.cancel()
		}
		<-done
	}

	for _, c := range s.channels {
		if err := c.Close(ctx); err != nil {
			s.logger.Warn("notify: channel close error", "channel", c.ID(), "error", err)
		}
	}

	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}

	s.logger.Info("notify stopped",
		"dropped_ingress", s.droppedIngress.Load(),
		"dropped_action", s.disp.DroppedActionCount())
	return nil
}

// onBusEvent is invoked synchronously from the bus publisher's goroutine. The
// hot path is a single non-blocking send into ingressCh; backpressure here
// drops the event and increments a counter. This is the boundary between
// "running tasks" (must never block) and "delivering notifications" (best
// effort).
func (s *Service) onBusEvent(e events.Event) {
	ev := MapEvent(e)
	if ev == nil {
		return
	}
	select {
	case s.ingressCh <- ev:
	default:
		s.droppedIngress.Add(1)
	}
}

// runDispatch pulls events from ingressCh and hands them to the dispatcher.
// Exits when ingressCh is closed (Stop) or ctx is cancelled.
func (s *Service) runDispatch(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-s.ingressCh:
			if !ok {
				return
			}
			s.disp.dispatch(ev)
		}
	}
}

// runRetention runs the configured pruning function on a ticker. Exits when
// ctx is cancelled. The function itself is constructed at wiring time (the
// builder closes over the storage repo + retention limits).
func (s *Service) runRetention(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(s.retentionEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.retentionFn(ctx)
		}
	}
}

// DroppedIngressCount reports the cumulative count of events dropped at the
// bus → service boundary. Diagnostic-only.
func (s *Service) DroppedIngressCount() uint64 {
	return s.droppedIngress.Load()
}
