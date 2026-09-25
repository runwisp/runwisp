// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"errors"

	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/runwisp/runwisp/internal/storage"
)

// mapDomainError translates a runService sentinel (or storage.ErrNotFound) to
// the matching huma error. Handlers funnel every error through this helper so
// the HTTP status mapping for "task not found", "service not runnable", etc.
// lives in exactly one place. Errors that aren't recognised collapse to a
// generic 500 with the supplied fallback message — callers should pass a
// short, action-shaped phrase like "Failed to trigger run".
//
// ctx is the request context: when the client disconnects mid-query (a browser
// aborting a superseded fetch, navigating away), the in-flight SQLite query is
// interrupted and surfaces as a driver error — but that is a client
// cancellation, not a server fault. We detect it via ctx and return 408 without
// logging, rather than manufacturing a 500 whose cause we'd then have to spam.
func mapDomainError(ctx context.Context, err error, fallback500 string) huma.StatusError {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return huma.Error408RequestTimeout("Request cancelled")
	}
	switch {
	case errors.Is(err, ErrTaskNotFound),
		errors.Is(err, ErrRunNotFound),
		errors.Is(err, storage.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrManualTriggerDisabled):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, ErrServiceNotRunnable),
		errors.Is(err, ErrCannotDeleteActiveRun),
		errors.Is(err, ErrRestartDidNotDrain):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrNotRunning),
		errors.Is(err, ErrInvalidSelector),
		errors.Is(err, ErrInvalidParams):
		return huma.Error400BadRequest(err.Error())
	default:
		if fallback500 == "" {
			fallback500 = "Internal server error"
		}
		// Never let a 500's cause vanish: the public message is intentionally
		// generic, so the real error only survives if we log it here.
		slog.Error(fallback500, "err", err)
		return huma.Error500InternalServerError(fallback500)
	}
}
