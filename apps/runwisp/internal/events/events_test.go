// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package events

import (
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestEventBus(t *testing.T) {
	t.Run("subscribe and publish sync", func(t *testing.T) {
		eb := NewEventBus()
		var received Event
		called := false

		unsub := eb.Subscribe(EventRunCreated, func(e Event) {
			received = e
			called = true
		})
		defer unsub()

		data := RunEvent{Run: &model.Run{ID: "test-data"}}
		eb.Publish(EventRunCreated, data)

		assert.True(t, called)
		assert.Equal(t, EventRunCreated, received.Type)
		assert.Equal(t, data, received.Data)
		assert.WithinDuration(t, time.Now(), received.Timestamp, time.Second)
	})

	t.Run("subscribe and publish async", func(t *testing.T) {
		eb := NewEventBus()
		wg := sync.WaitGroup{}
		wg.Add(1)

		var received Event
		unsub := eb.Subscribe(EventRunCreated, func(e Event) {
			received = e
			wg.Done()
		})
		defer unsub()

		data := RunEvent{Run: &model.Run{ID: "test-data"}}
		eb.Publish(EventRunCreated, data)

		wg.Wait()
		assert.Equal(t, EventRunCreated, received.Type)
		assert.Equal(t, data, received.Data)
	})

	t.Run("unsubscribe", func(t *testing.T) {
		eb := NewEventBus()
		count := 0

		unsub := eb.Subscribe(EventRunCreated, func(e Event) {
			count++
		})

		eb.Publish(EventRunCreated, RunEvent{Run: &model.Run{ID: "1"}})
		assert.Equal(t, 1, count)

		unsub()

		eb.Publish(EventRunCreated, RunEvent{Run: &model.Run{ID: "2"}})
		assert.Equal(t, 1, count)
	})

	t.Run("subscribe all", func(t *testing.T) {
		eb := NewEventBus()
		count := 0
		mu := sync.Mutex{}

		unsub := eb.SubscribeAll(func(e Event) {
			mu.Lock()
			defer mu.Unlock()
			count++
		})
		defer unsub()

		eb.Publish(EventRunCreated, RunEvent{Run: &model.Run{ID: "1"}})
		eb.Publish(EventRunStarted, RunEvent{Run: &model.Run{ID: "2"}})
		eb.Publish(EventLogLine, LogLineEvent{RunID: "3", LineNum: 0, Stream: "stdout", Text: "hello"})

		assert.Equal(t, 3, count)
	})

	t.Run("subscribe all unsubscribe", func(t *testing.T) {
		eb := NewEventBus()
		count := 0

		unsub := eb.SubscribeAll(func(e Event) {
			count++
		})

		eb.Publish(EventRunCreated, RunEvent{Run: &model.Run{ID: "1"}})
		assert.Equal(t, 1, count)

		unsub()

		eb.Publish(EventRunCreated, RunEvent{Run: &model.Run{ID: "2"}})
		assert.Equal(t, 1, count)
	})

	t.Run("multiple subscribers", func(t *testing.T) {
		eb := NewEventBus()
		count1 := 0
		count2 := 0

		eb.Subscribe(EventRunCreated, func(e Event) {
			count1++
		})
		eb.Subscribe(EventRunCreated, func(e Event) {
			count2++
		})

		eb.Publish(EventRunCreated, RunEvent{Run: &model.Run{ID: "1"}})

		assert.Equal(t, 1, count1)
		assert.Equal(t, 1, count2)
	})

	t.Run("remove cleanup", func(t *testing.T) {
		eb := NewEventBus().(*defaultEventBus)
		unsub := eb.Subscribe(EventRunCreated, func(e Event) {})

		eb.mu.RLock()
		_, ok := eb.subscribers[EventRunCreated]
		eb.mu.RUnlock()
		assert.True(t, ok)

		unsub()

		eb.mu.RLock()
		_, ok = eb.subscribers[EventRunCreated]
		eb.mu.RUnlock()
		assert.False(t, ok)
	})
}
