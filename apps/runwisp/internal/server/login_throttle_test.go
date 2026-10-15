// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginDelay(t *testing.T) {
	want := []time.Duration{0, 0, 0, 0, 0, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 64 * time.Second, 90 * time.Second, 90 * time.Second}
	for failures, d := range want {
		assert.Equal(t, d, loginDelay(failures), "failures=%d", failures)
	}
	assert.Equal(t, maxLoginDelay, loginDelay(1000), "no shift overflow")
}

// fail records one wrong password for ip, failing the test if it was throttled.
func fail(t *testing.T, th *loginThrottle, ip string) {
	t.Helper()
	done, wait := th.acquire(ip)
	require.NotNil(t, done, "throttled for %s", wait)
	done(loginWrongPassword)
}

func TestLoginThrottle_DelayAndMemory(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	th := newLoginThrottle(func() time.Time { return now })

	for range freeLoginFailures {
		fail(t, th, "a")
	}
	done, wait := th.acquire("a")
	assert.Nil(t, done)
	assert.Equal(t, 2*time.Second, wait)

	now = now.Add(2 * time.Second)
	fail(t, th, "a")
	_, wait = th.acquire("a")
	assert.Equal(t, 4*time.Second, wait, "each failure past the free ones doubles the wait")

	now = now.Add(loginFailureMemory + time.Second)
	done, _ = th.acquire("a")
	require.NotNil(t, done, "failures are forgotten after loginFailureMemory")
	done(loginWrongPassword)
	assert.Equal(t, 1, th.ips["a"].failures)
}

func TestLoginThrottle_UncheckedNeitherCountsNorResets(t *testing.T) {
	th := newLoginThrottle(time.Now)
	fail(t, th, "a")
	for range 3 * freeLoginFailures {
		done, _ := th.acquire("a")
		require.NotNil(t, done)
		done(loginUnchecked)
	}
	assert.Equal(t, 1, th.ips["a"].failures)

	done, _ := th.acquire("b")
	done(loginUnchecked)
	assert.NotContains(t, th.ips, "b", "an IP with nothing to remember is dropped")
}

// A parallel burst from one IP must not outrun the throttle: with each check
// slow enough that every request would start before the first failure is
// recorded, only the free failures get a password check.
func TestLoginThrottle_BurstIsSerialized(t *testing.T) {
	th := newLoginThrottle(time.Now)
	var checked atomic.Int32
	var wg sync.WaitGroup
	for range 4 * freeLoginFailures {
		wg.Go(func() {
			done, _ := th.acquire("a")
			if done == nil {
				return
			}
			checked.Add(1)
			time.Sleep(5 * time.Millisecond)
			done(loginWrongPassword)
		})
	}
	wg.Wait()
	assert.EqualValues(t, freeLoginFailures, checked.Load())
}

func TestLoginThrottle_SweepDropsForgottenIPs(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	th := newLoginThrottle(func() time.Time { return now })
	for i := range throttleSweepAt {
		fail(t, th, strconv.Itoa(i))
	}
	now = now.Add(loginFailureMemory + time.Second)
	fail(t, th, "fresh")
	assert.Len(t, th.ips, 1)
}
