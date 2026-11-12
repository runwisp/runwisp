// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/runwisp/runwisp/apps/runwisp/internal/backoff"
	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/logarchive"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

const (
	// handshakeTimeout bounds writing hello and waiting for welcome.
	handshakeTimeout = 20 * time.Second
	// stationTooOldRetry is how long the daemon waits before trying again a
	// station that doesn't speak this protocol: it won't until it's upgraded.
	stationTooOldRetry = 5 * time.Minute
	// tasksSyncTimeout bounds one tasks:sync round trip.
	tasksSyncTimeout = 30 * time.Second
)

// errStationTooOld ends a connection attempt to a station that didn't select
// the runwisp.v1 subprotocol.
var errStationTooOld = errors.New("station too old for this daemon: it does not speak protocol " + Subprotocol)

type Dependencies struct {
	TaskManager       TaskRunner
	RunRepo           ExternalRunGetter
	PendingUploadRepo storage.PendingLogUploadRepository
	Outbox            storage.StationOutboxRepository
	// Credentials persists a token the station rotates in (agent:credentials).
	// nil refuses rotation.
	Credentials CredentialStore
	EventBus    EventSubscriber
	// LocalTasks is the daemon's TOML-defined task set, re-read on every
	// sync so a `runwisp reload` is reflected.
	LocalTasks   *runtime.TaskRegistry
	LogDir       string
	Availability executor.Availability
	// ProbeAvailability is Availability with the container engines checked
	// (executor.RoutingExecutor.ProbedAvailability); nil keeps Availability.
	ProbeAvailability func(ctx context.Context) executor.Availability
	OnConnected       func()
	// Timezone returns the live [daemon] timezone (an IANA name, kept current by
	// reloads), the zone synced schedules default to. nil or an unloadable name
	// falls back to UTC.
	Timezone func() string
	// RequestRestart restarts the daemon process (agent:restart). nil means
	// the daemon can't restart itself.
	RequestRestart func() error
	// CanRestart reports why agent:restart would be refused, or nil when it
	// would restart. Advertised as feature agent.restart.
	CanRestart func() error
	// Upgrade installs the given release tag over the running binary
	// (agent:upgrade) and returns the restart that switches onto it, run once
	// the result is on the wire.
	Upgrade func(ctx context.Context, tag string) (restart func(), err error)
	// CanUpgrade reports why agent:upgrade would be refused, or nil when it
	// would upgrade. Advertised as feature agent.upgrade. nil (with Upgrade
	// nil) means upgrades are unsupported.
	CanUpgrade func() error
	// SystemStats returns a live host snapshot for agent:stats.
	SystemStats func() model.SystemStats
	// InstanceName returns the live [daemon] name, reported in hello.
	InstanceName func() string
	// Now is the wall-clock source for persisted timestamps and notAfter.
	// Production wires time.Now; tests inject a fixed clock.
	Now func() time.Time
}

type Client struct {
	config Config
	deps   Dependencies

	handler  *InboundHandler
	tracker  *ExecutionTracker
	outbox   *outbox
	uploader *LogUploader
	bridge   *EventBridge
	// startedAt marks the daemon's start for agent:stats uptime.
	startedAt time.Time

	mu       sync.Mutex
	session  *session // the session past welcome; nil while disconnected
	token    string
	everSeen bool // a session reached welcome at least once

	// syncMu serialises task syncs so a stale catalog never lands after a
	// newer one. syncedHash is the catalog the station has, guarded by syncMu.
	syncMu     sync.Mutex
	syncedHash string
	warnedZone string // last invalid daemon zone already warned about; guarded by syncMu
	reloadCh   chan struct{}
	// upgradeMu lets one agent:upgrade run at a time.
	upgradeMu sync.Mutex
}

func NewClient(cfg Config, deps Dependencies) (*Client, error) {
	if deps.TaskManager == nil {
		return nil, fmt.Errorf("station client requires task manager")
	}
	if deps.RunRepo == nil {
		return nil, fmt.Errorf("station client requires run repository")
	}
	if deps.EventBus == nil {
		return nil, fmt.Errorf("station client requires event bus")
	}
	if deps.Outbox == nil {
		return nil, fmt.Errorf("station client requires an outbox")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}

	client := &Client{
		config:    cfg,
		deps:      deps,
		tracker:   NewExecutionTracker(),
		outbox:    newOutbox(deps.Outbox),
		startedAt: deps.Now(),
		token:     cfg.StationToken,
		reloadCh:  make(chan struct{}, 1),
	}
	client.uploader = NewLogUploader(deps.PendingUploadRepo, deps.RunRepo, deps.LogDir, deps.Now, client.requestUploads)
	client.uploader.TrustHost(logarchive.HostPort(cfg.BaseURL))
	if cfg.AllowInsecure {
		client.uploader.AllowInsecure()
	}
	client.handler = NewInboundHandler(InboundHandlerDeps{
		TaskManager:  deps.TaskManager,
		RunRepo:      deps.RunRepo,
		LogDir:       deps.LogDir,
		Availability: deps.Availability,
		LocalTasks:   deps.LocalTasks,
		AppendUpdate: client.outbox.append,
		Uploader:     client.uploader,
		Tracker:      client.tracker,
		Now:          deps.Now,
	})
	client.bridge = NewEventBridge(client)
	slog.Info("station integration enabled", "baseURL", cfg.BaseURL.String(), "fingerprint", cfg.Fingerprint)
	return client, nil
}

// RecoverArchiveBacklog runs at boot, before Run. Every execution still
// waiting for its log archive whose run already ended (typically it ended
// while the daemon was down, or the daemon crashed before reporting it) gets
// its finished update stored again; the station applies it idempotently. The
// archives themselves upload after the next welcome.
func (client *Client) RecoverArchiveBacklog(ctx context.Context) {
	for _, executionID := range client.uploader.LoadPending(ctx) {
		run, err := client.deps.RunRepo.GetRunByExecutionID(ctx, executionID)
		if err != nil || run == nil || !run.Status.IsTerminal() {
			continue
		}
		client.outbox.append(executionUpdate(run))
	}
}

func (client *Client) Run(ctx context.Context) error {
	client.bridge.Start(ctx)
	defer client.bridge.Shutdown()
	defer client.watchReloads(ctx)()

	backoff := newReconnectBackoff()
	for {
		if ctx.Err() != nil {
			return nil
		}
		stable, err := client.connectOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if stable {
			backoff.Reset()
		}

		delay, err := reconnectDelay(err, backoff)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

// reconnectDelay is how long to wait after a session ended with err, or the
// error that stops the client for good.
func reconnectDelay(err error, retry *backoff.Exponential) (time.Duration, error) {
	goodbye, isGoodbye := errors.AsType[*goodbyeError](err)
	switch {
	case isGoodbye && !goodbye.Retry:
		slog.Error("station ended the session and asked this daemon not to reconnect; the station integration is stopped until the daemon restarts",
			"code", goodbye.Code, "message", goodbye.Message)
		return 0, err
	case isGoodbye:
		delay := goodbye.delay()
		if delay == 0 {
			delay = retry.Next()
		}
		slog.Info("station ended the session", "code", goodbye.Code, "message", goodbye.Message, "reconnectIn", delay.String())
		return delay, nil
	case errors.Is(err, errStationTooOld):
		slog.Error("station too old for this daemon: it does not speak protocol "+Subprotocol+"; upgrade the station", "retryIn", stationTooOldRetry.String())
		return stationTooOldRetry, nil
	}
	if err != nil {
		slog.Warn("station connection attempt ended", "error", err.Error())
	}
	delay := retry.Next()
	slog.Info("reconnecting", "delay", delay.String())
	return delay, nil
}

// connectOnce dials, says hello, and runs the session the station accepts
// until it ends. stable reports the session lasted long enough to reset the
// reconnect backoff: a peer that drops every session at once would otherwise
// turn the exponential curve into a tight loop.
func (client *Client) connectOnce(ctx context.Context) (stable bool, err error) {
	client.probeAvailability(ctx)
	conn, err := client.dial(ctx)
	if err != nil {
		return false, err
	}
	if conn.Subprotocol() != Subprotocol {
		_ = conn.Close(websocket.StatusPolicyViolation, "unsupported protocol")
		return false, errStationTooOld
	}
	conn.SetReadLimit(maxInboundFrameBytes)

	hello := client.buildHello(ctx)
	welcome, err := handshake(ctx, conn, hello)
	if err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return false, err
	}

	client.syncMu.Lock()
	if welcome.TasksHashKnown {
		client.syncedHash = hello.TasksHash
	} else {
		client.syncedHash = ""
	}
	client.syncMu.Unlock()

	sess := newSession(ctx, client, conn, welcome, hello)
	start := time.Now()
	err = sess.run(ctx)
	return time.Since(start) >= minStableSessionDuration, err
}

func (client *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+client.currentToken())
	dialCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, client.config.WebSocketURL(), &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	return conn, nil
}

// handshake sends hello and waits for the station's welcome. A goodbye comes
// back as a *goodbyeError.
func handshake(ctx context.Context, conn *websocket.Conn, hello protocol.Hello) (protocol.Welcome, error) {
	hsCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	frame, err := encodeFrame(typeHello, "", "", hello)
	if err != nil {
		return protocol.Welcome{}, err
	}
	if err := conn.Write(hsCtx, websocket.MessageText, frame); err != nil {
		return protocol.Welcome{}, fmt.Errorf("send hello: %w", err)
	}
	for {
		_, payload, err := conn.Read(hsCtx)
		if err != nil {
			return protocol.Welcome{}, fmt.Errorf("wait for welcome: %w", err)
		}
		var env protocol.Envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return protocol.Welcome{}, fmt.Errorf("malformed frame before welcome: %w", err)
		}
		switch env.Type {
		case typeWelcome:
			return decodeData[protocol.Welcome](env)
		case typeGoodbye:
			g, err := decodeData[protocol.Goodbye](env)
			if err != nil {
				return protocol.Welcome{}, err
			}
			return protocol.Welcome{}, &goodbyeError{g}
		default:
			// Nothing else may precede welcome; skip it rather than guess.
			slog.Debug("ignoring frame before welcome", "type", env.Type)
		}
	}
}

// availabilityProbeTimeout bounds the container engine checks before hello.
const availabilityProbeTimeout = 2 * time.Second

// probeAvailability re-checks the container engines, so hello reports
// exec.container and exec.compose unavailable on a host without Docker.
// ponytail: probed only on connect; an engine that starts or stops mid-session
// shows at the next reconnect. Re-probe on a timer if that proves too stale.
func (client *Client) probeAvailability(ctx context.Context) {
	if client.deps.ProbeAvailability == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, availabilityProbeTimeout)
	defer cancel()
	client.handler.setAvailability(client.deps.ProbeAvailability(ctx))
}

// buildHello describes this daemon: identity, features, catalog hash and
// every execution it still owns.
func (client *Client) buildHello(ctx context.Context) protocol.Hello {
	_, hash := client.taskCatalog()
	return protocol.Hello{
		InstanceID:    client.config.Fingerprint,
		Agent:         client.agentInfo(),
		Features:      client.features(),
		Labels:        map[string]string{},
		MaxConcurrent: 0,
		TasksHash:     hash,
		LastSeq:       client.outbox.lastSeq(ctx),
		Executions:    client.tracker.Inventory(client.outbox.executions(ctx)),
	}
}

// attach makes sess the session live sends go to.
func (client *Client) attach(sess *session) {
	client.mu.Lock()
	first := !client.everSeen
	client.session, client.everSeen = sess, true
	client.mu.Unlock()
	if first && client.deps.OnConnected != nil {
		client.deps.OnConnected()
	}
}

func (client *Client) detach(sess *session) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.session == sess {
		client.session = nil
	}
}

// current returns the attached session, nil while disconnected.
func (client *Client) current() *session {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.session
}

func (client *Client) currentToken() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.token
}

// requestUploads asks the station for upload targets over the attached
// session. It fails at once while disconnected; the caller retries later.
func (client *Client) requestUploads(ctx context.Context, req protocol.UploadRequest) (protocol.UploadRequestResult, error) {
	sess := client.current()
	if sess == nil {
		return protocol.UploadRequestResult{}, &StationError{Code: protocol.ErrorCodeUnavailable, Message: "not connected to the station", Retryable: true}
	}
	env, err := sess.command(ctx, typeUploadRequest, req)
	if err != nil {
		return protocol.UploadRequestResult{}, err
	}
	return decodeResult[protocol.UploadRequestResult](env)
}

// watchReloads reacts to tasks.changed (a `runwisp reload`, or a service
// starting or stopping): it re-syncs the catalog when its hash changed and
// tells the station about changed features or name. Returns the stop func.
func (client *Client) watchReloads(ctx context.Context) (stop func()) {
	unsubscribe := client.deps.EventBus.Subscribe(events.EventTasksChanged, func(events.Event) {
		select {
		case client.reloadCh <- struct{}{}:
		default: // one is already pending; it reads the latest state
		}
	})
	loopCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer crashguard.Guard()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-client.reloadCh:
				if sess := client.current(); sess != nil {
					sess.sendAgentUpdateIfChanged()
					client.syncTasks(sess, false)
				}
			}
		}
	}()
	return func() {
		unsubscribe()
		cancel()
	}
}

// syncTasks sends tasks:sync when the catalog differs from the one the station
// has (or always, with force). A failure is only logged: the next hello
// carries the hash again and the station asks for the catalog then.
func (client *Client) syncTasks(sess *session, force bool) {
	client.syncMu.Lock()
	defer client.syncMu.Unlock()
	defs, hash := client.taskCatalogLocked()
	if !force && hash == client.syncedHash {
		return
	}
	ctx, cancel := context.WithTimeout(sess.ctx, tasksSyncTimeout)
	defer cancel()
	env, err := sess.command(ctx, typeTasksSync, protocol.TasksSync{Hash: hash, Tasks: defs})
	if err == nil {
		var summary protocol.TasksSyncResult
		if summary, err = decodeResult[protocol.TasksSyncResult](env); err == nil {
			client.syncedHash = hash
			slog.Info("task catalog synced", "added", summary.Added, "updated", summary.Updated, "removed", summary.Removed, "total", summary.Total)
			return
		}
	}
	if sess.ctx.Err() == nil {
		slog.Warn("task sync failed; the next reconnect retries", "error", err.Error())
	}
}

// taskCatalog returns the tasks:sync rows and their hash.
func (client *Client) taskCatalog() ([]protocol.TaskDefinition, string) {
	client.syncMu.Lock()
	defer client.syncMu.Unlock()
	return client.taskCatalogLocked()
}

func (client *Client) taskCatalogLocked() ([]protocol.TaskDefinition, string) {
	defs := buildTaskDefinitions(client.snapshotForSync(), client.daemonZone())
	return defs, tasksHash(defs)
}

// daemonZone is the zone tasks without their own timezone are synced in. The
// station loads it with time.LoadLocation, so an empty or unloadable name
// (never produced by config load, which resolves the host zone to an IANA
// name) falls back to UTC, with one warning per distinct bad value. Callers
// hold syncMu.
func (client *Client) daemonZone() string {
	if client.deps.Timezone == nil {
		return "UTC"
	}
	name := client.deps.Timezone()
	if _, err := time.LoadLocation(name); err == nil && name != "" && name != "Local" {
		return name
	}
	if client.warnedZone != name {
		client.warnedZone = name
		slog.Warn("daemon timezone is not an IANA name the station can load; syncing schedules in UTC", "timezone", name)
	}
	return "UTC"
}

// snapshotForSync builds the task set reported in tasks:sync: the TOML tasks
// plus every supervised service the task manager holds that the TOML doesn't
// cover. The extras are station-declared services (service:apply, named
// "station-<id>"); reporting them tells the station they are live here, which
// gates their deletion. One-shot station executions are left out by the
// ListServiceTasks filter: they are never a live task on this runner.
func (client *Client) snapshotForSync() map[string]*model.Task {
	snapshot := map[string]*model.Task{}
	if client.deps.LocalTasks != nil {
		for name, task := range client.deps.LocalTasks.Snapshot() {
			if task != nil {
				snapshot[name] = task
			}
		}
	}
	for _, svc := range client.deps.TaskManager.ListServiceTasks() {
		if svc == nil {
			continue
		}
		if _, ok := snapshot[svc.Name]; !ok {
			snapshot[svc.Name] = svc
		}
	}
	return snapshot
}

// goodbyeError ends a session the station closed with goodbye.
type goodbyeError struct{ protocol.Goodbye }

func (g *goodbyeError) Error() string {
	return fmt.Sprintf("station said goodbye: %s: %s", g.Code, g.Message)
}

// delay is how long to wait before reconnecting: retryAfterMs plus jitter, or
// for draining a random time in [0, retryAfterMs] so a fleet spreads out. 0
// means the station named no wait; the normal backoff applies.
func (g *goodbyeError) delay() time.Duration {
	after := time.Duration(g.RetryAfterMs) * time.Millisecond
	if after <= 0 {
		return 0
	}
	if g.Code == protocol.GoodbyeCodeDraining {
		return rand.N(after + 1)
	}
	return after + rand.N(time.Duration(float64(after)*reconnectJitterFraction)+1)
}
