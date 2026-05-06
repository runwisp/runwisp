// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import "context"

// Channel is the unit the dispatcher pumps events through. Implementations:
// slack, telegram, inapp. Filtering is the Router's job; channels only
// execute and clean up.
type Channel interface {
	// ID is a stable, log-friendly identifier — the user-supplied notifier id.
	ID() string

	// Execute performs the side effect. Implementations must respect ctx
	// (cancel, deadline) and return promptly when ctx is done.
	Execute(ctx context.Context, ev *Event) error

	// Close releases resources associated with the channel.
	Close(ctx context.Context) error
}
