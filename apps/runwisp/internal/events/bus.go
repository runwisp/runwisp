// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package events

// EventBus is the interface for the event pub/sub hub.
// Using this interface instead of the concrete implementation makes consumer packages
// testable without a live event bus.
type EventBus interface {
	Subscribe(eventType EventType, handler EventHandler) func()
	SubscribeAll(handler EventHandler) func()
	Publish(eventType EventType, data EventData)
	PublishSync(eventType EventType, data EventData)
}
