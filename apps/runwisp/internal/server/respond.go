// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/runwisp/runwisp/internal/storage"
	"log/slog"
)

// mapDomainError translates a runService sentinel (or storage.ErrNotFound) to
// the matching huma error. Handlers funnel every error through this helper so
// the HTTP status mapping for "task not found", "service not runnable", etc.
// lives in exactly one place. Errors that aren't recognised collapse to a
// generic 500 with the supplied fallback message — callers should pass a
// short, action-shaped phrase like "Failed to trigger run".
func mapDomainError(err error, fallback500 string) huma.StatusError {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrTaskNotFound),
		errors.Is(err, ErrRunNotFound),
		errors.Is(err, storage.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrAPIDisabled):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, ErrServiceNotRunnable),
		errors.Is(err, ErrCannotDeleteActiveRun):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrNotAService),
		errors.Is(err, ErrNotRunning),
		errors.Is(err, ErrInvalidSelector):
		return huma.Error400BadRequest(err.Error())
	default:
		if fallback500 == "" {
			fallback500 = "Internal server error"
		}
		return huma.Error500InternalServerError(fallback500)
	}
}

func respondError(resp http.ResponseWriter, status int, publicMsg string, err error) {
	if err != nil && status >= http.StatusInternalServerError {
		slog.Error(publicMsg, "err", err)
	}
	http.Error(resp, publicMsg, status)
}

func respondBadRequest(resp http.ResponseWriter, publicMsg string) {
	respondError(resp, http.StatusBadRequest, publicMsg, nil)
}

func respondNotFound(resp http.ResponseWriter, publicMsg string) {
	respondError(resp, http.StatusNotFound, publicMsg, nil)
}

func respondForbidden(resp http.ResponseWriter, publicMsg string) {
	respondError(resp, http.StatusForbidden, publicMsg, nil)
}

func respondUnauthorized(resp http.ResponseWriter, publicMsg string) {
	respondError(resp, http.StatusUnauthorized, publicMsg, nil)
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("Failed to encode JSON response", "err", err)
	}
}
