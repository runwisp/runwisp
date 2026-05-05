// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import "context"

// Channel adds Type and Close lifecycle methods to Action. Channel closes
// are best-effort: HTTP-backed providers cancel in-flight requests via the
// supplied context; the in-app channel flushes its hub.
type Channel interface {
	Action
	Type() string
	Close(ctx context.Context) error
}
