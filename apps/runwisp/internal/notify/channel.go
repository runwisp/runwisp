// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import "context"

// Channel is the unit the dispatcher pumps events through. Implementations:
// slack, telegram, inapp. The dispatcher and router only care about ID/Match/
// Execute; Close is part of the Service shutdown sequence so HTTP-backed
// channels can release pooled connections.
type Channel interface {
	// ID is a stable, log-friendly identifier — the user-supplied notifier id.
	ID() string

	// Match is a cheap predicate that lets a channel filter events further than
	// the router already did. False here causes the dispatcher to skip it
	// without enqueuing.
	Match(*Event) bool

	// Execute performs the side effect. Implementations must respect ctx
	// (cancel, deadline) and return promptly when ctx is done.
	Execute(ctx context.Context, ev *Event) error

	// Close releases resources associated with the channel.
	Close(ctx context.Context) error
}
