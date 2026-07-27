// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package events

// EventBus is the interface for the event pub/sub hub.
// Using this interface instead of the concrete implementation makes consumer packages
// testable without a live event bus.
//
// Publish invokes each handler synchronously on the caller's goroutine; handlers
// that need async fan-out (e.g. SSE streaming) must own their own buffered
// channel. This keeps the bus allocation-free per publish.
type EventBus interface {
	Subscribe(eventType EventType, handler EventHandler) func()
	SubscribeAll(handler EventHandler) func()
	Publish(eventType EventType, data EventData)
}
