// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
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

	// throttleSweepAt is the map size that triggers a sweep of forgotten IPs.
	throttleSweepAt = 1024
)

// loginThrottle slows password guessing per client IP. Only a wrong password
// counts: a bad nonce or launch ticket never tests the password, so it can't
// help a guesser. It also runs one password check per IP at a time, so a burst
// of parallel guesses can't all start before the first failure is recorded.
// Other IPs never wait on each other.
type loginThrottle struct {
	now func() time.Time

	mu  sync.Mutex
	ips map[string]*loginState
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
	loginSucceeded     loginOutcome = iota // forgets the IP's failures
	loginWrongPassword                     // counts as a failure
	loginUnchecked                         // bad nonce: the password was never tested
)

func newLoginThrottle(now func() time.Time) *loginThrottle {
	return &loginThrottle{now: now, ips: map[string]*loginState{}}
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

// acquire waits for ip's turn to check a password. It returns done, which the
// caller must call with the outcome, or a nil done and the time left when ip
// must wait before trying again.
func (t *loginThrottle) acquire(ip string) (done func(loginOutcome), retryAfter time.Duration) {
	t.mu.Lock()
	st := t.ips[ip]
	if st == nil {
		if len(t.ips) >= throttleSweepAt {
			t.sweepLocked()
		}
		st = &loginState{}
		t.ips[ip] = st
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
		t.releaseLocked(ip, st)
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
		case loginUnchecked:
		}
		t.releaseLocked(ip, st)
	}, 0
}

func (t *loginThrottle) releaseLocked(ip string, st *loginState) {
	st.turn.Unlock()
	if st.refs--; st.refs == 0 && st.failures == 0 {
		delete(t.ips, ip)
	}
}

// sweepLocked drops idle IPs whose failures are past loginFailureMemory.
// ponytail: O(n) scan once the map reaches throttleSweepAt; a guesser rotating
// through many /64s within loginFailureMemory still grows it, as httprate did.
func (t *loginThrottle) sweepLocked() {
	now := t.now()
	for ip, st := range t.ips {
		if st.refs == 0 && now.Sub(st.lastFail) > loginFailureMemory {
			delete(t.ips, ip)
		}
	}
}
