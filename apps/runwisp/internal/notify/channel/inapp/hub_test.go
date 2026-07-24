// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package inapp

import (
	"sync"
	"testing"

	"github.com/runwisp/runwisp/internal/storage"
)

// TestHubConcurrentPublishUnsubscribe drives Subscribe/unsubscribe churn
// against a steady stream of Publish calls. On the pre-fix Hub, a Publish that
// snapshots the subscriber set and then sends outside the lock could land on a
// channel that a concurrent unsubscribe had just closed, panicking with "send
// on closed channel" (not saved by the select's default case). The fixed Hub
// sends under the read lock, mutually exclusive with the close, so this runs to
// completion without panicking.
func TestHubConcurrentPublishUnsubscribe(t *testing.T) {
	hub := NewHub(1, 0)

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
				}
			}
		}()
	}

	var subWg sync.WaitGroup
	for s := 0; s < 8; s++ {
		subWg.Add(1)
		go func() {
			defer subWg.Done()
			for i := 0; i < 3000; i++ {
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

	if got := hub.SubscriberCount(); got != 0 {
		t.Fatalf("expected all subscribers removed, got %d", got)
	}
}
