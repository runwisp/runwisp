// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"sync"
	"time"
)

const (
	// freeLoginFailures wrong passwords per client IP cost nothing; each one
	// after that doubles the wait before the next try, from 2s up to
	// maxLoginDelay. A typo never locks anyone out for long, while a guesser
	// is held to one try per maxLoginDelay (960 a day per IP).
	freeLoginFailures = 5
	maxLoginDelay     = 90 * time.Second

	// loginFailureMemory is how long an IP's failures are remembered after its
	// last one. A successful login forgets them at once.
	loginFailureMemory = 15 * time.Minute

	// maxChallengesPerMinute caps challenge nonces minted per IP, so one client
	// can't churn the bounded nonce store. Every login needs one, so the cap
	// sits far above what a person or a script logging in would ever use.
	maxChallengesPerMinute = 60

	// maxConcurrentPasswordChecks caps password checks running at once across
	// every client. Each is a 600k-round PBKDF2 (two with a viewer password),
	// and the per-IP throttle can't stop many addresses (an IPv6 /48 holds 65k
	// /64s, each with its free tries) from keeping every core busy and starving
	// the tasks. A login past the cap waits up to passwordCheckWait for a slot.
	// Browsers that signed in before get their own slot (knownDeviceChecks),
	// so a flood from new clients never keeps them out.
	maxConcurrentPasswordChecks = 2
	knownDeviceChecks           = 1
	passwordCheckWait           = 10 * time.Second

	// throttleSweepAt is the map size that triggers a sweep of forgotten IPs.
	throttleSweepAt = 1024
)

// loginThrottle slows password guessing per client: per IP, or per device for
// a browser holding a device cookie from an earlier login, so a guesser on the
// same IP or proxy can't slow down a browser that already knows the password.
// Only a wrong password
// counts: a bad nonce or launch ticket never tests the password, so it can't
// help a guesser. It also runs one password check per IP at a time, so a burst
// of parallel guesses can't all start before the first failure is recorded.
// Other IPs never wait on each other.
type loginThrottle struct {
	now func() time.Time

	checks      chan struct{} // global password-check slots, see maxConcurrentPasswordChecks
	knownChecks chan struct{} // the same, reserved for known devices
	checkWait   time.Duration

	mu      sync.Mutex
	clients map[string]*loginState
}

type loginState struct {
	turn sync.Mutex // held for the length of one password check

	// Guarded by loginThrottle.mu.
	refs     int // requests holding or queued for turn
	failures int
	lastFail time.Time
}

// loginOutcome is what a login attempt told the throttle.
type loginOutcome int

const (
	loginSucceeded     loginOutcome = iota // operator password: forgets the client's failures
	loginWrongPassword                     // counts as a failure
	loginUnchecked                         // bad nonce: the password was never tested
	loginViewer                            // viewer password: leaves the count alone, so it can't reset guesses at the operator one
)

func newLoginThrottle(now func() time.Time) *loginThrottle {
	return &loginThrottle{
		now:         now,
		checks:      make(chan struct{}, maxConcurrentPasswordChecks),
		knownChecks: make(chan struct{}, knownDeviceChecks),
		checkWait:   passwordCheckWait,
		clients:     map[string]*loginState{},
	}
}

// checkSlot waits for a global password-check slot, from the reserved pool
// when knownDevice. It returns release, which the caller must call once the
// check is over, or nil when no slot freed up in time or ctx ended first.
func (t *loginThrottle) checkSlot(ctx context.Context, knownDevice bool) (release func()) {
	slots := t.checks
	if knownDevice {
		slots = t.knownChecks
	}
	wait := time.NewTimer(t.checkWait)
	defer wait.Stop()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }
	case <-wait.C:
	case <-ctx.Done():
	}
	return nil
}

// loginDelay is the wait required after a client's failures-th wrong password.
func loginDelay(failures int) time.Duration {
	if failures < freeLoginFailures {
		return 0
	}
	if failures-freeLoginFailures >= 6 { // 2s<<6 already passes the cap
		return maxLoginDelay
	}
	return min(2*time.Second<<(failures-freeLoginFailures), maxLoginDelay)
}

// acquire waits for client's turn to check a password; client is an IP or a
// device key. It returns done, which the caller must call with the outcome, or
// a nil done and the time left when client must wait before trying again.
func (t *loginThrottle) acquire(client string) (done func(loginOutcome), retryAfter time.Duration) {
	t.mu.Lock()
	st := t.clients[client]
	if st == nil {
		if len(t.clients) >= throttleSweepAt {
			t.sweepLocked()
		}
		st = &loginState{}
		t.clients[client] = st
	}
	st.refs++
	t.mu.Unlock()

	st.turn.Lock()

	t.mu.Lock()
	now := t.now()
	if st.failures > 0 && now.Sub(st.lastFail) > loginFailureMemory {
		st.failures = 0
	}
	if wait := st.lastFail.Add(loginDelay(st.failures)).Sub(now); wait > 0 {
		t.releaseLocked(client, st)
		t.mu.Unlock()
		return nil, wait
	}
	t.mu.Unlock()

	return func(outcome loginOutcome) {
		t.mu.Lock()
		defer t.mu.Unlock()
		switch outcome {
		case loginSucceeded:
			st.failures = 0
		case loginWrongPassword:
			st.failures++
			st.lastFail = t.now()
		case loginUnchecked, loginViewer:
		}
		t.releaseLocked(client, st)
	}, 0
}

func (t *loginThrottle) releaseLocked(client string, st *loginState) {
	st.turn.Unlock()
	if st.refs--; st.refs == 0 && st.failures == 0 {
		delete(t.clients, client)
	}
}

// sweepLocked drops idle clients whose failures are past loginFailureMemory.
// ponytail: O(n) scan once the map reaches throttleSweepAt; a guesser rotating
// through many /64s within loginFailureMemory still grows it, as httprate did.
func (t *loginThrottle) sweepLocked() {
	now := t.now()
	for client, st := range t.clients {
		if st.refs == 0 && now.Sub(st.lastFail) > loginFailureMemory {
			delete(t.clients, client)
		}
	}
}
