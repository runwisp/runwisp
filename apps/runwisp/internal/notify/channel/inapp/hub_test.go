// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package inapp

import (
	"runtime"
	"sync"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// TestHubConcurrentPublishUnsubscribe drives Subscribe/unsubscribe churn
// against a steady stream of Publish calls. A Publish that sent outside the lock
// could land on a channel a concurrent unsubscribe had just closed and panic
// with "send on closed channel"; Hub sends under the read lock, mutually
// exclusive with the close, so this runs to completion without panicking.
func TestHubConcurrentPublishUnsubscribe(t *testing.T) {
	hub := NewHub(1)

	stop := make(chan struct{})
	var pubWg sync.WaitGroup
	for p := 0; p < 4; p++ {
		pubWg.Add(1)
		go func() {
			defer pubWg.Done()
			u := Update{Type: UpdateTypeCreated, Notification: storage.Notification{ID: "01ABCDEF"}}
			for {
				select {
				case <-stop:
					return
				default:
					hub.Publish(u)
					// Without a yield the spinning readers starve unsubscribe's
					// write lock on small CI runners (minutes under -race).
					runtime.Gosched()
				}
			}
		}()
	}

	var subWg sync.WaitGroup
	for s := 0; s < 8; s++ {
		subWg.Add(1)
		go func() {
			defer subWg.Done()
			for i := 0; i < 500; i++ {
				sub, unsub := hub.Subscribe()
				select {
				case <-sub.Channel():
				default:
				}
				unsub()
			}
		}()
	}

	subWg.Wait()
	close(stop)
	pubWg.Wait()

	if got := len(hub.subs); got != 0 {
		t.Fatalf("expected all subscribers removed, got %d", got)
	}
}

// TestHubPublishDropsOldestNotNewest pins drop-oldest semantics: when a
// subscriber's buffer is full, the oldest update is evicted so the newest
// (carrying the authoritative UnreadCount) still reaches it rather than leaving
// the subscriber with a stale count.
func TestHubPublishDropsOldestNotNewest(t *testing.T) {
	hub := NewHub(1)
	sub, unsub := hub.Subscribe()
	defer unsub()

	hub.Publish(Update{Type: UpdateTypeUnreadCountChanged, UnreadCount: 1}) // fills the buffer
	hub.Publish(Update{Type: UpdateTypeUnreadCountChanged, UnreadCount: 2}) // must evict #1, keep #2

	got := <-sub.Channel()
	if got.UnreadCount != 2 {
		t.Fatalf("drop-oldest: got UnreadCount %d, want the newest (2)", got.UnreadCount)
	}
}
