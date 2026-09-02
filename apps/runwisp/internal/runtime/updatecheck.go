// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"net/http"
	"sync"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/update"
)

// DefaultUpdateCheckInterval is how often the daemon polls GitHub for a newer
// release at steady state. Rare on purpose — a new release is a slow-moving fact
// and this is a background, best-effort, opt-out-able outbound call.
const DefaultUpdateCheckInterval = 6 * time.Hour

// updateCheckTimeout bounds a single poll so a hung GitHub connection can never
// pile up goroutines across ticks.
const updateCheckTimeout = 15 * time.Second

// UpdateStatus is the cached result of the most recent successful check.
type UpdateStatus struct {
	Available bool
	Latest    string
	CheckedAt time.Time
}

// UpdateChecker periodically asks GitHub for the newest release and caches
// whether it is newer than the running build. Best-effort: a failed check (no
// network, rate limit) is logged at debug and leaves the last good result in
// place — it never surfaces as an error and never blocks anything. Construct it
// only for release builds (see update.IsRelease).
type UpdateChecker struct {
	current  string
	interval time.Duration
	// fetch resolves the latest release tag; a field so tests inject a stub
	// instead of hitting the network.
	fetch  func(context.Context) (string, error)
	cancel context.CancelFunc

	mu     sync.Mutex
	status UpdateStatus
}

// NewUpdateChecker builds a checker for the given running version. client is used
// for the outbound GitHub call (nil transport honors HTTP(S)_PROXY).
func NewUpdateChecker(current string, client *http.Client) *UpdateChecker {
	return &UpdateChecker{
		current:  current,
		interval: DefaultUpdateCheckInterval,
		fetch: func(ctx context.Context) (string, error) {
			return update.LatestRelease(ctx, client)
		},
	}
}

func (c *UpdateChecker) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	// One check shortly after boot so the indicator is fresh without waiting a
	// full interval; its own goroutine so Start never blocks daemon startup.
	go c.checkOnce(ctx)
	startTicker(ctx, c.interval, "Stopping update checker", c.checkOnce)
}

func (c *UpdateChecker) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *UpdateChecker) checkOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	latest, err := c.fetch(ctx)
	if err != nil {
		slog.Debug("update check failed", "err", err)
		return
	}
	available := update.Compare(c.current, latest) < 0

	c.mu.Lock()
	first := c.status.CheckedAt.IsZero()
	c.status = UpdateStatus{Available: available, Latest: latest, CheckedAt: time.Now()}
	c.mu.Unlock()

	if available && first {
		slog.Info("a newer RunWisp release is available",
			"current", c.current, "latest", latest, "hint", "update via the Web UI/TUI or re-run the installer")
	}
}

// Status returns the last cached check result for /api/daemon. Zero-value
// (Available=false, empty Latest) until the first successful check.
func (c *UpdateChecker) Status() (available bool, latest string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status.Available, c.status.Latest
}
